package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

// lines is a fake Searchable over plain-text files: one message per line,
// "session<TAB>text". Files ending in .append are append-only.
type lines struct{ dir string }

func (l lines) Coverage() adapter.Coverage { return adapter.CoverageFull }

func (l lines) Transcripts(context.Context) ([]adapter.Transcript, []session.Warning) {
	files, _ := filepath.Glob(filepath.Join(l.dir, "*"))
	var out []adapter.Transcript
	for _, f := range files {
		if strings.HasSuffix(f, "-wal") {
			continue // a companion, not a transcript
		}
		tr := adapter.Transcript{Path: f, Append: strings.HasSuffix(f, ".append")}
		if strings.HasSuffix(f, ".db") {
			tr.Companions = []string{f + "-wal"}
		}
		out = append(out, tr)
	}
	return out, nil
}

func (l lines) Extract(ctx context.Context, t adapter.Transcript, from int64, emit func(adapter.Message)) (int64, error) {
	return adapter.ScanFrom(ctx, t.Path, from, func(raw []byte) {
		sid, text, _ := strings.Cut(string(raw), "\t")
		emit(adapter.Message{SessionID: sid, Role: "user", Text: text})
	})
}

func setup(t *testing.T) (*Index, string, []Source) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o755); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(filepath.Join(dir, "cache", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix, data, []Source{{Engine: "fake", A: lines{dir: data}}}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, ix *Index) int {
	t.Helper()
	st, err := ix.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.Messages
}

func ids(hits []Hit) string {
	var s []string
	for _, h := range hits {
		s = append(s, h.SessionID)
	}
	return strings.Join(s, ",")
}

func TestIndexIsPrivate(t *testing.T) {
	ix, _, _ := setup(t)
	fi, err := os.Stat(ix.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("index mode = %v, want 0600 (it holds prompts)", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(ix.Path()))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("index dir mode = %v, want 0700", di.Mode().Perm())
	}
}

func TestAppendOnlyFilesAreReadIncrementally(t *testing.T) {
	ix, data, src := setup(t)
	ctx := context.Background()
	p := filepath.Join(data, "a.append")
	write(t, p, "s1\tdeploy the api\ns1\trollback plan\n")
	if st, _ := ix.Update(ctx, src, nil); st.Messages != 2 {
		t.Fatalf("first update: %+v", st)
	}
	// a half-written last line must wait for its newline
	appendTo(t, p, "s1\tcanary release\ns1\tunfinished")
	if st, _ := ix.Update(ctx, src, nil); st.Messages != 1 {
		t.Fatalf("append: want only the new complete line, got %+v", st)
	}
	appendTo(t, p, " line\n")
	ix.Update(ctx, src, nil)
	if n := count(t, ix); n != 4 {
		t.Fatalf("want 4 messages without duplicates, got %d", n)
	}
	if hits, _ := ix.Search(ctx, "unfinished line", ""); len(hits) != 1 {
		t.Fatalf("completed line not searchable: %v", hits)
	}
	// unchanged file: nothing re-read
	if st, _ := ix.Update(ctx, src, nil); st.Changed != 0 {
		t.Fatalf("unchanged: %+v", st)
	}
}

func TestRewrittenAndDeletedFilesAreReplaced(t *testing.T) {
	ix, data, src := setup(t)
	ctx := context.Background()
	p := filepath.Join(data, "g.json") // not append-only
	write(t, p, "s2\told wording\n")
	ix.Update(ctx, src, nil)
	write(t, p, "s2\tnew wording entirely\n")
	ix.Update(ctx, src, nil)
	if hits, _ := ix.Search(ctx, "old", ""); len(hits) != 0 {
		t.Fatalf("stale text survived a rewrite: %v", hits)
	}
	if n := count(t, ix); n != 1 {
		t.Fatalf("want 1 message after rewrite, got %d", n)
	}
	// a shrunk append-only file is re-read from scratch too
	q := filepath.Join(data, "c.append")
	write(t, q, "s3\tfirst\ns3\tsecond\n")
	ix.Update(ctx, src, nil)
	write(t, q, "s3\tthird\n")
	ix.Update(ctx, src, nil)
	if hits, _ := ix.Search(ctx, "first", ""); len(hits) != 0 {
		t.Fatalf("truncated file kept old messages")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if st, _ := ix.Update(ctx, src, nil); st.Removed != 1 {
		t.Fatalf("deleted file not dropped: %+v", st)
	}
	if hits, _ := ix.Search(ctx, "wording", ""); len(hits) != 0 {
		t.Fatalf("deleted file still searchable")
	}
}

// A SQLite database takes new rows in its write-ahead log first; a change
// there must re-read the transcript although the database file is untouched.
func TestCompanionChangesAreNoticed(t *testing.T) {
	ix, data, src := setup(t)
	ctx := context.Background()
	db := filepath.Join(data, "c.db")
	write(t, db, "s1\told\n")
	if _, ws := ix.Update(ctx, src, nil); len(ws) > 0 {
		t.Fatal(ws)
	}
	st, _ := os.Stat(db)
	write(t, db, "s1\tnew\n") // same size…
	if err := os.Chtimes(db, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err) // …and time: only the log below shows the change
	}
	write(t, db+"-wal", "log")
	if _, ws := ix.Update(ctx, src, nil); len(ws) > 0 {
		t.Fatal(ws)
	}
	hits, err := ix.Search(ctx, "new", "")
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits %v err %v", hits, err)
	}
}

func TestSearchSemantics(t *testing.T) {
	ix, data, src := setup(t)
	ctx := context.Background()
	write(t, filepath.Join(data, "m"), strings.Join([]string{
		"alpha\tНастрой Vault токен в Keychain",
		"alpha\tтокен протух",
		"alpha\tvault again",
		"beta\tvault only",
		"gamma\tdeployment pipeline is slow",
		"delta\tweird \"quotes\" AND NEAR( - operators",
	}, "\n")+"\n")
	ix.Update(ctx, src, nil)

	cases := []struct{ q, want string }{
		{"ТОКЕН", "alpha"},              // Cyrillic, case-insensitive
		{"vault токен", "alpha"},        // every word must match
		{"deploy", "gamma"},             // last word matches as a prefix
		{`"quotes" AND NEAR(`, "delta"}, // operators are literal text
	}
	for _, c := range cases {
		hits, err := ix.Search(ctx, c.q, "")
		if err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		if got := ids(hits); got != c.want {
			t.Errorf("%q: got %q, want %q", c.q, got, c.want)
		}
	}
	// one hit per session, counting all its matches
	hits, _ := ix.Search(ctx, "vault", "")
	if ids(hits) != "beta,alpha" && ids(hits) != "alpha,beta" {
		t.Fatalf("vault: %v", ids(hits))
	}
	for _, h := range hits {
		if h.SessionID == "alpha" && h.Matches != 2 {
			t.Errorf("alpha matches = %d, want 2", h.Matches)
		}
		if !strings.Contains(h.Snippet, MarkStart) {
			t.Errorf("snippet has no highlight: %q", h.Snippet)
		}
	}
	if _, err := ix.Search(ctx, `  "" `, ""); err != ErrEmptyQuery {
		t.Errorf("empty query: %v", err)
	}
}

func TestSchemaChangeRebuilds(t *testing.T) {
	ix, data, src := setup(t)
	ctx := context.Background()
	write(t, filepath.Join(data, "m"), "s\thello\n")
	ix.Update(ctx, src, nil)
	if _, err := ix.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	path := ix.Path()
	ix.Close()
	ix2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ix2.Close()
	if n := count(t, ix2); n != 0 {
		t.Fatalf("old-version index not dropped: %d", n)
	}
}

func TestHighlight(t *testing.T) {
	got := Highlight("Deploy preview per PR", []string{"deploy", "pr"})
	want := MarkStart + "Deploy" + MarkEnd + " " + MarkStart + "pr" + MarkEnd + "eview per " + MarkStart + "PR" + MarkEnd
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := Highlight("Токен в Keychain", []string{"токен"}); got != MarkStart+"Токен"+MarkEnd+" в Keychain" {
		t.Fatalf("cyrillic: %q", got)
	}
}
