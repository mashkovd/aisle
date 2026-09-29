// Package search keeps a local full-text index of conversation text in
// SQLite FTS5. The index is a cache: it can be deleted at any time and is
// rebuilt from the agents' own files.
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

// schemaVersion is bumped whenever extraction or schema changes; a mismatch
// drops and rebuilds the index.
const schemaVersion = 2

// Snippet highlight markers; renderers replace them.
const (
	MarkStart = "\x02"
	MarkEnd   = "\x03"
)

type Index struct {
	db   *sql.DB
	path string
}

// DefaultPath honours $AISLE_INDEX, else <user cache dir>/aisle/index.db.
func DefaultPath() string {
	if p := os.Getenv("AISLE_INDEX"); p != "" {
		return p
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "aisle", "index.db")
}

// Open opens or creates the index. The directory is 0700 and the database
// 0600: it holds copies of prompts, which can contain secrets.
func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
	} else {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; reads are short
	ix := &Index{db: db, path: path}
	if err := ix.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("index %s: %w", path, err)
	}
	return ix, nil
}

func (ix *Index) Close() error { return ix.db.Close() }
func (ix *Index) Path() string { return ix.path }

func (ix *Index) migrate() error {
	var v int
	if err := ix.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v == schemaVersion {
		return nil
	}
	stmts := []string{
		`DROP TRIGGER IF EXISTS messages_ai`, `DROP TRIGGER IF EXISTS messages_ad`,
		`DROP TABLE IF EXISTS fts`, `DROP TABLE IF EXISTS messages`, `DROP TABLE IF EXISTS files`,
		`CREATE TABLE files (path TEXT PRIMARY KEY, engine TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL, offset INTEGER NOT NULL)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY, path TEXT NOT NULL, engine TEXT NOT NULL, session_id TEXT NOT NULL, role TEXT NOT NULL, ts INTEGER NOT NULL, text TEXT NOT NULL)`,
		`CREATE INDEX messages_path ON messages(path)`,
		`CREATE VIRTUAL TABLE fts USING fts5(text, content='messages', content_rowid='id', tokenize='unicode61 remove_diacritics 2')`,
		`CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN INSERT INTO fts(rowid, text) VALUES (new.id, new.text); END`,
		`CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN INSERT INTO fts(fts, rowid, text) VALUES ('delete', old.id, old.text); END`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion),
	}
	for _, s := range stmts {
		if _, err := ix.db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// Source is one searchable adapter.
type Source struct {
	Engine string
	A      adapter.Searchable
}

type fileState struct {
	size, mtime, offset int64
}

type job struct {
	engine string
	t      adapter.Transcript
	from   int64
	reset  bool // drop the file's messages before inserting
	size   int64
	mtime  int64
}

type extracted struct {
	job
	msgs []adapter.Message
	end  int64
	err  error
}

// UpdateStats reports what one Update did.
type UpdateStats struct {
	Files    int // transcripts considered
	Changed  int // transcripts (re)read
	Removed  int // transcripts that disappeared
	Messages int // messages added
}

// Update brings the index up to date with the agents' files. progress, if
// set, is called after each changed transcript is written.
func (ix *Index) Update(ctx context.Context, sources []Source, progress func(done, total int)) (UpdateStats, []session.Warning) {
	var (
		st    UpdateStats
		warns []session.Warning
	)
	known := map[string]fileState{}
	rows, err := ix.db.QueryContext(ctx, `SELECT path, size, mtime, offset FROM files`)
	if err != nil {
		return st, []session.Warning{{Adapter: "index", Path: ix.path, Message: err.Error()}}
	}
	for rows.Next() {
		var p string
		var fs fileState
		if err := rows.Scan(&p, &fs.size, &fs.mtime, &fs.offset); err == nil {
			known[p] = fs
		}
	}
	rows.Close()

	current := map[string]bool{}
	var jobs []job
	for _, src := range sources {
		ts, ws := src.A.Transcripts(ctx)
		warns = append(warns, ws...)
		for _, t := range ts {
			info, err := os.Stat(t.Path)
			if err != nil {
				continue
			}
			current[t.Path] = true
			st.Files++
			size, mtime := info.Size(), info.ModTime().UnixNano()
			for _, c := range t.Companions {
				if ci, err := os.Stat(c); err == nil {
					size += ci.Size()
					mtime = max(mtime, ci.ModTime().UnixNano())
				}
			}
			prev, seen := known[t.Path]
			switch {
			case seen && prev.size == size && prev.mtime == mtime:
				continue
			case seen && t.Append && size >= prev.size:
				jobs = append(jobs, job{engine: src.Engine, t: t, from: prev.offset, size: size, mtime: mtime})
			default:
				jobs = append(jobs, job{engine: src.Engine, t: t, reset: seen, size: size, mtime: mtime})
			}
		}
	}

	for p := range known {
		if !current[p] {
			if err := ix.dropFile(ctx, p); err != nil {
				warns = append(warns, session.Warning{Adapter: "index", Path: p, Message: err.Error()})
				continue
			}
			st.Removed++
		}
	}

	// extract in parallel, write from this goroutine only
	byEngine := map[string]adapter.Searchable{}
	for _, s := range sources {
		byEngine[s.Engine] = s.A
	}
	in := make(chan job)
	out := make(chan extracted)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range in {
				var msgs []adapter.Message
				end, err := byEngine[j.engine].Extract(ctx, j.t, j.from, func(m adapter.Message) { msgs = append(msgs, m) })
				out <- extracted{job: j, msgs: msgs, end: end, err: err}
			}
		}()
	}
	go func() {
		defer close(in)
		for _, j := range jobs {
			select {
			case in <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()

	done := 0
	for r := range out {
		done++
		if r.err != nil {
			warns = append(warns, session.Warning{Adapter: r.engine, Path: r.t.Path, Message: "not indexed: " + r.err.Error()})
		} else if err := ix.write(ctx, r); err != nil {
			warns = append(warns, session.Warning{Adapter: "index", Path: r.t.Path, Message: err.Error()})
		} else {
			st.Changed++
			st.Messages += len(r.msgs)
		}
		if progress != nil {
			progress(done, len(jobs))
		}
	}
	return st, warns
}

func (ix *Index) dropFile(ctx context.Context, path string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE path = ?`, path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

func (ix *Index) write(ctx context.Context, r extracted) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.reset {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE path = ?`, r.t.Path); err != nil {
			return err
		}
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO messages (path, engine, session_id, role, ts, text) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, m := range r.msgs {
		sid := m.SessionID
		if sid == "" {
			sid = r.t.SessionID
		}
		if sid == "" {
			continue
		}
		var ts int64
		if !m.Time.IsZero() {
			ts = m.Time.Unix()
		}
		if _, err := ins.ExecContext(ctx, r.t.Path, r.engine, sid, m.Role, ts, m.Text); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO files (path, engine, size, mtime, offset) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, offset = excluded.offset`,
		r.t.Path, r.engine, r.size, r.mtime, r.end); err != nil {
		return err
	}
	return tx.Commit()
}

// Hit is the best match within one session.
type Hit struct {
	Engine    string    `json:"engine"`
	SessionID string    `json:"id"`
	Role      string    `json:"role"`
	Snippet   string    `json:"snippet"`
	Time      time.Time `json:"time"`
	Matches   int       `json:"matches"`
	Rank      float64   `json:"rank"` // bm25: lower is better
}

// MatchExpr turns free text into an FTS5 query: every word must occur, and
// the last word also matches as a prefix so results follow typing.
func MatchExpr(text string) string {
	words := strings.Fields(text)
	var parts []string
	// drop tokens with nothing searchable (bare quotes, dashes, brackets)
	kept := words[:0]
	for _, w := range words {
		if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			kept = append(kept, w)
		}
	}
	words = kept
	for i, w := range words {
		w = strings.ReplaceAll(w, `"`, `""`)
		q := `"` + w + `"`
		if i == len(words)-1 && len([]rune(w)) >= 2 {
			q += "*"
		}
		parts = append(parts, q)
	}
	return strings.Join(parts, " ")
}

// Highlight wraps case-insensitive occurrences of words in text with the
// snippet markers, matching what FTS snippets look like.
func Highlight(text string, words []string) string {
	lower := strings.ToLower(text)
	if len(lower) != len(text) {
		return text // case mapping changed byte offsets; show unmarked
	}
	marks := make([]bool, len(text))
	for _, w := range words {
		for i := 0; w != ""; {
			j := strings.Index(lower[i:], w)
			if j < 0 {
				break
			}
			for k := i + j; k < i+j+len(w); k++ {
				marks[k] = true
			}
			i += j + len(w)
		}
	}
	var b strings.Builder
	in := false
	for i := 0; i < len(text); i++ {
		if marks[i] != in {
			if marks[i] {
				b.WriteString(MarkStart)
			} else {
				b.WriteString(MarkEnd)
			}
			in = marks[i]
		}
		b.WriteByte(text[i])
	}
	if in {
		b.WriteString(MarkEnd)
	}
	return b.String()
}

// ErrEmptyQuery is returned for a query without searchable words.
var ErrEmptyQuery = errors.New("empty search query")

// maxRows bounds how many matching messages are ranked per query.
const maxRows = 5000

// Search returns the best hit per session, best first.
func (ix *Index) Search(ctx context.Context, text, engine string) ([]Hit, error) {
	expr := MatchExpr(text)
	if expr == "" {
		return nil, ErrEmptyQuery
	}
	q := `SELECT m.engine, m.session_id, m.role, m.ts,
	             snippet(fts, 0, char(2), char(3), '…', 14), bm25(fts)
	      FROM fts JOIN messages m ON m.id = fts.rowid
	      WHERE fts MATCH ?`
	args := []any{expr}
	if engine != "" {
		q += ` AND m.engine = ?`
		args = append(args, engine)
	}
	q += fmt.Sprintf(` ORDER BY bm25(fts) LIMIT %d`, maxRows)
	rows, err := ix.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	best := map[string]*Hit{}
	var order []string
	for rows.Next() {
		var h Hit
		var ts int64
		if err := rows.Scan(&h.Engine, &h.SessionID, &h.Role, &ts, &h.Snippet, &h.Rank); err != nil {
			return nil, err
		}
		key := h.Engine + ":" + h.SessionID
		if b, ok := best[key]; ok {
			b.Matches++
			continue
		}
		if ts > 0 {
			h.Time = time.Unix(ts, 0)
		}
		h.Snippet = strings.Join(strings.Fields(h.Snippet), " ")
		h.Matches = 1
		best[key] = &h
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(order))
	for _, k := range order {
		hits = append(hits, *best[k])
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Rank < hits[j].Rank })
	return hits, nil
}

// Stats describes the index for doctor.
type Stats struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Files    int    `json:"files"`
	Messages int    `json:"messages"`
}

func (ix *Index) Stats(ctx context.Context) (Stats, error) {
	s := Stats{Path: ix.path}
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(ix.path + suffix); err == nil {
			s.Bytes += fi.Size()
		}
	}
	if err := ix.db.QueryRowContext(ctx, `SELECT count(*) FROM files`).Scan(&s.Files); err != nil {
		return s, err
	}
	err := ix.db.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&s.Messages)
	return s, err
}

// Purge deletes the index database and its WAL files.
func Purge(path string) error {
	var errs []error
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
