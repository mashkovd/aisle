package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"

	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
)

func TestWordFilterKeepsOrderAndNeedsAllWords(t *testing.T) {
	targets := []string{
		"c1 claude /work/api Deploy pipeline fix",
		"x1 codex /work/web add dark mode",
		"g1 gemini /work/api explain the deploy",
		"a1 agy /work/java Assess Java longevity",
	}
	got := WordFilter("deploy api", targets)
	if len(got) != 2 || got[0].Index != 0 || got[1].Index != 2 {
		t.Fatalf("got %+v", got)
	}
	if len(WordFilter("zzz", targets)) != 0 {
		t.Fatal("no fuzzy matches expected")
	}
	if n := len(WordFilter("", targets)); n != 4 {
		t.Fatalf("empty term keeps everything, got %d", n)
	}
}

func TestWaitingSessionsComeFirstAndCursorStays(t *testing.T) {
	now := time.Now()
	snap := service.Snapshot{Sessions: []session.Session{
		{Engine: "claude", NativeID: "a", UpdatedAt: now, Runtime: &session.Runtime{Name: "ta", Status: "idle"}},
		{Engine: "claude", NativeID: "b", UpdatedAt: now.Add(-time.Hour), Runtime: &session.Runtime{Name: "tb", Status: "working"}},
		{Engine: "claude", NativeID: "c", UpdatedAt: now.Add(-2 * time.Hour)},
	}}
	m := model{snap: snap, letters: map[string]string{"claude": "c"}, now: now}
	m.list = list.New(nil, list.NewDefaultDelegate(), 80, 40)
	m.setItems()
	m.list.Select(0) // "a"
	m.applyStatus(statusMsg{"tb": "asking"})
	if first := m.list.Items()[0].(sessionItem).s.NativeID; first != "b" {
		t.Errorf("first is %q, want the session that needs you", first)
	}
	if sel := m.list.SelectedItem().(sessionItem).s.NativeID; sel != "a" {
		t.Errorf("cursor moved to %q", sel)
	}
	if m.list.Title != "aisle · 1 need you" {
		t.Errorf("title %q", m.list.Title)
	}
	m.applyStatus(statusMsg{"tb": "idle"})
	if first := m.list.Items()[0].(sessionItem).s.NativeID; first != "a" || m.list.Title != "aisle" {
		t.Errorf("after answering: first %q, title %q", first, m.list.Title)
	}
}
