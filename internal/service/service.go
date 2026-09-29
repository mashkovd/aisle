// Package service is the only place that decides how a session is opened:
// attach to its runtime when one exists, otherwise ask the adapter for a
// command and launch a new runtime. The TUI and CLI call nothing lower.
package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/runtime/proc"
	"github.com/mashkovd/aisle/internal/runtime/tmux"
	"github.com/mashkovd/aisle/internal/search"
	"github.com/mashkovd/aisle/internal/session"
	"github.com/mashkovd/aisle/internal/status"
	"github.com/mashkovd/aisle/internal/worktree"
)

type Service struct {
	Adapters []adapter.Adapter
	Tmux     *tmux.Client
	// Procs reads the process table; replaceable in tests.
	Procs func(context.Context) (proc.Table, error)

	mu      sync.Mutex
	screens map[string]string // last capture per tmux session, for Observe
}

// Snapshot is everything aisle knows at one moment.
type Snapshot struct {
	Sessions []session.Session `json:"sessions"`
	// Unlinked holds tmux sessions not tied to a discovered conversation:
	// foreign ones and aisle-started ones whose conversation has no history yet.
	Unlinked []session.Runtime `json:"unlinked_runtimes"`
	Warnings []session.Warning `json:"warnings,omitempty"`
}

func (s *Service) Adapter(name string) (adapter.Adapter, bool) {
	for _, a := range s.Adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// Discover runs every adapter concurrently and links aisle-managed runtimes.
func (s *Service) Discover(ctx context.Context) Snapshot {
	type result struct {
		ss []session.Session
		ws []session.Warning
	}
	results := make([]result, len(s.Adapters))
	var wg sync.WaitGroup
	for i, a := range s.Adapters {
		wg.Add(1)
		go func(i int, a adapter.Adapter) {
			defer wg.Done()
			ss, ws := a.Discover(ctx)
			results[i] = result{ss, ws}
		}(i, a)
	}
	wg.Wait()

	var snap Snapshot
	for _, r := range results {
		snap.Sessions = append(snap.Sessions, r.ss...)
		snap.Warnings = append(snap.Warnings, r.ws...)
	}
	rts, err := s.Tmux.List()
	if err != nil {
		snap.Warnings = append(snap.Warnings, session.Warning{Adapter: "tmux", Message: err.Error()})
	}
	if cur := s.Tmux.Current(); cur != "" {
		// the session aisle itself runs in is not a destination
		kept := rts[:0]
		for _, rt := range rts {
			if rt.Name != cur {
				kept = append(kept, rt)
			}
		}
		rts = kept
	}
	snap.Sessions, snap.Unlinked = session.Link(snap.Sessions, rts)
	snap.Sessions, snap.Unlinked = s.infer(ctx, snap.Sessions, snap.Unlinked)
	session.SortByRecent(snap.Sessions)
	sort.SliceStable(snap.Unlinked, func(i, j int) bool { return snap.Unlinked[i].Name < snap.Unlinked[j].Name })
	return snap
}

// infer links runtimes that carry no conversation label — tmux sessions
// aisle did not start, and new ones whose conversation had no ID yet — when
// a process in the pane runs an agent's resume command naming a discovered
// conversation. The command line is the proof; nothing else is guessed.
func (s *Service) infer(ctx context.Context, ss []session.Session, rts []session.Runtime) ([]session.Session, []session.Runtime) {
	var readers []adapter.Adapter
	for _, a := range s.Adapters {
		if _, ok := a.(adapter.ArgvReader); ok {
			readers = append(readers, a)
		}
	}
	candidates := 0
	for _, rt := range rts {
		if rt.NativeID == "" && rt.PID > 0 {
			candidates++
		}
	}
	if candidates == 0 || len(readers) == 0 {
		return ss, rts
	}
	table, err := s.procs()(ctx)
	if err != nil {
		return ss, rts
	}
	byKey := make(map[string]int, len(ss))
	for i := range ss {
		byKey[ss[i].Key()] = i
	}
	var rest []session.Runtime
	for _, rt := range rts {
		if rt.NativeID != "" || rt.PID <= 0 || !s.link(ss, byKey, rt, table.Tree(rt.PID), readers) {
			rest = append(rest, rt)
		}
	}
	return ss, rest
}

func (s *Service) link(ss []session.Session, byKey map[string]int, rt session.Runtime, procs []proc.Process, readers []adapter.Adapter) bool {
	for _, p := range procs {
		for _, a := range readers {
			if rt.Engine != "" && rt.Engine != a.Name() {
				continue // a labelled runtime only hosts its own engine
			}
			id, ok := a.(adapter.ArgvReader).SessionFromArgv(p.Argv)
			if !ok {
				continue
			}
			i, found := byKey[a.Name()+":"+id]
			if !found || ss[i].Runtime != nil {
				continue
			}
			r := rt
			r.Inferred = rt.Engine == "" // an aisle-started runtime stays managed
			r.Engine, r.NativeID = a.Name(), id
			ss[i].Runtime = &r
			ss[i].State = session.Live
			return true
		}
	}
	return false
}

// Observe sets the status of every runtime in snap that hosts a known
// agent. A pane counts as working when its screen changed since the
// previous call, so callers look twice, or keep calling.
func (s *Service) Observe(snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.screens == nil {
		s.screens = map[string]string{}
	}
	var table *proc.Table
	look := func(rt *session.Runtime) {
		screen, err := s.Tmux.Capture(rt.Name)
		if err != nil {
			rt.Status = ""
			return
		}
		cmd, _ := s.Tmux.Pane(rt.Name)
		if cmd != "" && rt.PID > 0 {
			// a shell in front is only "exited" when no agent process is left:
			// agents started through a shell wrapper also show a shell
			if table == nil {
				t, err := s.procs()(context.Background())
				table = &t
				if err != nil {
					table = nil
				}
			}
			if table != nil && runsAgent(*table, rt.PID, rt.Engine) {
				cmd = ""
			}
		}
		rt.Status = string(status.Classify(screen, s.screens[rt.Name], cmd))
		s.screens[rt.Name] = screen
	}
	for i := range snap.Sessions {
		if rt := snap.Sessions[i].Runtime; rt != nil {
			look(rt)
		}
	}
	for i := range snap.Unlinked {
		if snap.Unlinked[i].Engine != "" {
			look(&snap.Unlinked[i])
		}
	}
}

func (s *Service) procs() func(context.Context) (proc.Table, error) {
	if s.Procs != nil {
		return s.Procs
	}
	return proc.Read
}

// runsAgent reports whether engine's binary runs in the pane rooted at pid.
func runsAgent(t proc.Table, pid int, engine string) bool {
	for _, p := range t.Tree(pid) {
		if _, ok := adapter.AfterBinary(p.Argv, engine); ok {
			return true
		}
	}
	return false
}

// Live reports whether snap has any runtime Observe would look at.
func (snap Snapshot) Live() bool {
	for _, ss := range snap.Sessions {
		if ss.Runtime != nil {
			return true
		}
	}
	for _, rt := range snap.Unlinked {
		if rt.Engine != "" {
			return true
		}
	}
	return false
}

// Resume attaches to the session's runtime or launches one. On success it
// does not return: the process is replaced by tmux.
func (s *Service) Resume(sess session.Session) error {
	if sess.Runtime != nil {
		return s.Tmux.Attach(sess.Runtime.Name)
	}
	if !s.Tmux.Available() {
		return tmux.ErrNotInstalled
	}
	a, ok := s.Adapter(sess.Engine)
	if !ok {
		return fmt.Errorf("adapter %q is not enabled", sess.Engine)
	}
	name := tmux.Name(sess.Engine, sess.Project, sess.NativeID)
	if s.Tmux.Has(name) {
		// a runtime by that name exists but is not labelled for this
		// conversation (e.g. started before labels existed): do not reuse it
		name = s.Tmux.FreeName(name)
	}
	return s.Tmux.Launch(name, a.Resume(sess), sess.Engine, sess.NativeID)
}

// NewOptions shape a new session.
type NewOptions struct {
	// Worktree starts the agent in a new git worktree of dir's repository.
	Worktree bool
	Name     string // worktree name; empty picks one
	// Created is told about the worktree before the terminal is handed over.
	Created func(worktree.Worktree)
}

// New starts a fresh conversation of engine in dir.
func (s *Service) New(engine, dir string, o NewOptions) error {
	if !s.Tmux.Available() {
		return tmux.ErrNotInstalled
	}
	a, ok := s.Adapter(engine)
	if !ok {
		return fmt.Errorf("unknown or disabled engine %q", engine)
	}
	name := tmux.Name(engine, dir, "")
	if o.Worktree {
		wt, err := worktree.Create(dir, o.Name, engine)
		if err != nil {
			return err
		}
		if o.Created != nil {
			o.Created(wt)
		}
		dir = wt.Path
		name = tmux.Name(engine, wt.Repo, "") + "-" + strings.TrimPrefix(wt.Name, engine+"-")
	}
	cmd, id := a.New(dir), ""
	if ia, ok := a.(adapter.IDAssigner); ok {
		cmd, id = ia.NewWithID(dir)
	}
	return s.Tmux.Launch(s.Tmux.FreeName(name), cmd, engine, id)
}

// Attach hands the terminal to an existing runtime by name.
func (s *Service) Attach(name string) error { return s.Tmux.Attach(name) }

// Sources returns the enabled adapters that can feed the full-text index.
func (s *Service) Sources() []search.Source {
	var out []search.Source
	for _, a := range s.Adapters {
		if sa, ok := a.(adapter.Searchable); ok {
			out = append(out, search.Source{Engine: a.Name(), A: sa})
		}
	}
	return out
}

// Result is a search hit joined with the session it can resume.
type Result struct {
	Session session.Session `json:"session"`
	Hit     search.Hit      `json:"hit"`
}

// Search matches hits to discovered sessions. Hits in conversations that
// cannot be resumed (subagents, deleted histories) are counted, not listed.
func (s *Service) Search(ctx context.Context, ix *search.Index, snap Snapshot, text, engine string) ([]Result, int, error) {
	hits, err := ix.Search(ctx, text, engine)
	if err != nil {
		return nil, 0, err
	}
	byKey := make(map[string]session.Session, len(snap.Sessions))
	for _, ss := range snap.Sessions {
		byKey[ss.Key()] = ss
	}
	var (
		out     []Result
		orphans int
		seen    = map[string]bool{}
	)
	for _, h := range hits {
		ss, ok := byKey[h.Engine+":"+h.SessionID]
		if !ok {
			orphans++
			continue
		}
		seen[ss.Key()] = true
		out = append(out, Result{Session: ss, Hit: h})
	}
	// Titles are searchable too: they are the only text for conversations
	// whose content aisle cannot read. Title-only matches rank after text hits.
	words := strings.Fields(strings.ToLower(text))
	for _, ss := range snap.Sessions {
		if seen[ss.Key()] || (engine != "" && ss.Engine != engine) || !containsAll(strings.ToLower(ss.Summary), words) {
			continue
		}
		out = append(out, Result{Session: ss, Hit: search.Hit{
			Engine: ss.Engine, SessionID: ss.NativeID, Role: "title", Matches: 1,
			Snippet: search.Highlight(ss.Summary, words), Time: ss.UpdatedAt,
		}})
	}
	return out, orphans, nil
}

func containsAll(s string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return len(words) > 0
}
