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
	"github.com/mashkovd/aisle/internal/runtime/tmux"
	"github.com/mashkovd/aisle/internal/search"
	"github.com/mashkovd/aisle/internal/session"
)

type Service struct {
	Adapters []adapter.Adapter
	Tmux     *tmux.Client
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
	session.SortByRecent(snap.Sessions)
	sort.SliceStable(snap.Unlinked, func(i, j int) bool { return snap.Unlinked[i].Name < snap.Unlinked[j].Name })
	return snap
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

// New starts a fresh conversation of engine in dir.
func (s *Service) New(engine, dir string) error {
	if !s.Tmux.Available() {
		return tmux.ErrNotInstalled
	}
	a, ok := s.Adapter(engine)
	if !ok {
		return fmt.Errorf("unknown or disabled engine %q", engine)
	}
	name := s.Tmux.FreeName(tmux.Name(engine, dir, ""))
	return s.Tmux.Launch(name, a.New(dir), engine, "")
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
