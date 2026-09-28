package session

import (
	"strings"
	"testing"
	"time"
)

func TestLinkIsDeterministicOnly(t *testing.T) {
	ss := []Session{
		{Engine: "claude", NativeID: "aaa", Project: "/p"},
		{Engine: "codex", NativeID: "bbb", Project: "/p"},
	}
	rts := []Runtime{
		{Name: "claude-p-aaa", Engine: "claude", NativeID: "aaa"}, // managed, matches
		{Name: "claude-p", Engine: "claude"},                      // managed, new session, no id yet
		{Name: "foo", Path: "/p", Command: "codex"},               // foreign: never guessed
		{Name: "gone", Engine: "gemini", NativeID: "zzz"},         // managed, history missing
	}
	linked, rest := Link(ss, rts)
	if linked[0].Runtime == nil || linked[0].Runtime.Name != "claude-p-aaa" || linked[0].State != Live {
		t.Errorf("managed runtime not linked: %+v", linked[0])
	}
	if linked[1].Runtime != nil {
		t.Errorf("foreign tmux must not be linked heuristically: %+v", linked[1])
	}
	if len(rest) != 3 {
		t.Errorf("want 3 unlinked runtimes, got %+v", rest)
	}
}

func TestResolve(t *testing.T) {
	ss := []Session{
		{Engine: "claude", NativeID: "abc111"},
		{Engine: "codex", NativeID: "abc222"},
		{Engine: "gemini", NativeID: "fff"},
	}
	if s, err := Resolve(ss, "fff"); err != nil || s.Engine != "gemini" {
		t.Errorf("exact: %v %v", s, err)
	}
	if _, err := Resolve(ss, "abc"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("want ambiguity, got %v", err)
	}
	if s, err := Resolve(ss, "codex:abc"); err != nil || s.NativeID != "abc222" {
		t.Errorf("qualified: %v %v", s, err)
	}
	if _, err := Resolve(ss, "zzz"); err == nil {
		t.Error("want no match")
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"@AGENTS.md  fix   the\nbug", 80, "fix the bug"},
		{"see [Pasted text #2 +40 lines] please", 80, "see please"},
		{"[Image #1] what is this", 80, "what is this"},
		{strings.Repeat("я", 100), 10, strings.Repeat("я", 9) + "…"}, // rune-safe
	}
	for _, c := range cases {
		if got := Summarize(c.in, c.max); got != c.want {
			t.Errorf("Summarize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		10 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 50 * time.Hour: "2d ago",
	}
	for d, want := range cases {
		if got := Ago(now.Add(-d), now); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}

func TestCountActionableIgnoresInfo(t *testing.T) {
	ws := []Warning{{Message: "unsupported format"}, {Message: "legacy", Info: true}, {Message: "read error"}}
	if n := CountActionable(ws); n != 2 {
		t.Fatalf("got %d", n)
	}
}
