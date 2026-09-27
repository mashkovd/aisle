// Package session holds the normalized model every adapter produces:
// conversation history → Session → optional Runtime.
package session

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type State string

const (
	Historical State = "historical"
	Live       State = "live"
)

// Source is one on-disk artifact a Session was assembled from.
type Source struct {
	Kind    string `json:"kind"`   // conversation | metadata | legacy
	Path    string `json:"path"`   //
	Format  string `json:"format"` // e.g. claude-jsonl-v1, gemini-chat-jsonl
	Partial bool   `json:"partial,omitempty"`
}

// Runtime is a live terminal host for a conversation. Only tmux exists today.
type Runtime struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Attached bool   `json:"attached"`
	Path     string `json:"path,omitempty"`
	Command  string `json:"command,omitempty"`
	// Engine and NativeID are set only on runtimes aisle launched itself.
	Engine   string `json:"engine,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// Managed reports whether aisle launched this runtime.
func (r Runtime) Managed() bool { return r.Engine != "" }

type Session struct {
	Engine    string    `json:"engine"`
	NativeID  string    `json:"id"`
	Project   string    `json:"project"`
	Summary   string    `json:"summary"`
	UpdatedAt time.Time `json:"updated_at"`
	State     State     `json:"state"`
	Sources   []Source  `json:"sources"`
	Runtime   *Runtime  `json:"runtime,omitempty"`
}

func (s Session) Key() string { return s.Engine + ":" + s.NativeID }

// Partial reports whether any source was read incompletely.
func (s Session) Partial() bool {
	for _, src := range s.Sources {
		if src.Partial {
			return true
		}
	}
	return false
}

// Warning is a non-fatal discovery problem. Adapters must emit one instead of
// silently dropping history they could not read.
type Warning struct {
	Adapter string `json:"adapter"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (w Warning) String() string {
	if w.Path == "" {
		return w.Adapter + ": " + w.Message
	}
	return fmt.Sprintf("%s: %s: %s", w.Adapter, w.Path, w.Message)
}

// SortByRecent orders sessions newest first; ties break on key for stable output.
func SortByRecent(ss []Session) {
	sort.SliceStable(ss, func(i, j int) bool {
		if !ss[i].UpdatedAt.Equal(ss[j].UpdatedAt) {
			return ss[i].UpdatedAt.After(ss[j].UpdatedAt)
		}
		return ss[i].Key() < ss[j].Key()
	})
}

// Link attaches aisle-managed runtimes to the sessions they host. Linking is
// deterministic only: a runtime matches when its @aisle_engine/@aisle_id
// labels equal the session's. Everything else is returned as unmanaged or
// unlinked and is never guessed at.
func Link(ss []Session, rts []Runtime) (linked []Session, rest []Runtime) {
	byKey := make(map[string]int, len(ss))
	for i := range ss {
		byKey[ss[i].Key()] = i
	}
	for _, rt := range rts {
		if rt.Managed() && rt.NativeID != "" {
			if i, ok := byKey[rt.Engine+":"+rt.NativeID]; ok && ss[i].Runtime == nil {
				r := rt
				ss[i].Runtime = &r
				ss[i].State = Live
				continue
			}
		}
		rest = append(rest, rt)
	}
	return ss, rest
}

// Resolve finds exactly one session by an ID prefix, optionally qualified as
// "engine:prefix".
func Resolve(ss []Session, query string) (Session, error) {
	engine, prefix := "", query
	if e, p, ok := strings.Cut(query, ":"); ok {
		engine, prefix = e, p
	}
	if prefix == "" {
		return Session{}, fmt.Errorf("empty session id")
	}
	var hits []Session
	for _, s := range ss {
		if engine != "" && s.Engine != engine {
			continue
		}
		if strings.HasPrefix(s.NativeID, prefix) {
			if s.NativeID == prefix {
				return s, nil
			}
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return Session{}, fmt.Errorf("no session matches %q", query)
	case 1:
		return hits[0], nil
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.Engine+":"+h.NativeID)
	}
	return Session{}, fmt.Errorf("%q is ambiguous: %s", query, strings.Join(ids, ", "))
}

var (
	leadingMention = regexp.MustCompile(`^@\S+\s*`)
	pastePlacehold = regexp.MustCompile(`\[(Pasted text #\d+( \+\d+ lines)?|Image #\d+)\]`)
)

// Summarize turns a raw prompt or title into a one-line label of at most max runes.
func Summarize(text string, max int) string {
	s := leadingMention.ReplaceAllString(strings.TrimSpace(text), "")
	s = pastePlacehold.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	if max > 1 && utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

// Ago renders a compact relative time: "just now", "5m ago", "3h ago", "2d ago", "Sep 02".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case t.Year() == now.Year():
		return t.Local().Format("Jan 02")
	}
	return t.Local().Format("Jan 02 2006")
}
