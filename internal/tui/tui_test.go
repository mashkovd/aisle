package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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

func snapWith(status string, extra ...session.Runtime) service.Snapshot {
	now := time.Now()
	return service.Snapshot{Sessions: []session.Session{
		{Engine: "claude", NativeID: "a", UpdatedAt: now, Runtime: &session.Runtime{Name: "ta", Status: "idle"}},
		{Engine: "claude", NativeID: "b", UpdatedAt: now.Add(-time.Hour), Runtime: &session.Runtime{Name: "tb", Status: status}},
		{Engine: "claude", NativeID: "c", UpdatedAt: now.Add(-2 * time.Hour)},
	}, Unlinked: extra}
}

func newModel(snap service.Snapshot) model {
	m := model{snap: snap, letters: map[string]string{"claude": "c"}, now: time.Now()}
	m.list = list.New(nil, list.NewDefaultDelegate(), 80, 40)
	m.list.Filter = WordFilter
	m.setItems()
	return m
}

func shownKeys(m model) string {
	var ks []string
	for _, it := range m.list.Items() {
		ks = append(ks, itemKey(it))
	}
	return strings.Join(ks, " ")
}

func TestWaitingSessionsComeFirstAndCursorStays(t *testing.T) {
	m := newModel(snapWith("working"))
	m.list.Select(0) // "a"
	m.applySnapshot(snapWith("asking"))
	if first := m.list.Items()[0].(sessionItem).s.NativeID; first != "b" {
		t.Errorf("first is %q, want the session that needs you", first)
	}
	if sel := m.list.SelectedItem().(sessionItem).s.NativeID; sel != "a" {
		t.Errorf("cursor moved to %q", sel)
	}
	if m.list.Title != "aisle · 1 need you" {
		t.Errorf("title %q", m.list.Title)
	}
	m.applySnapshot(snapWith("idle"))
	if first := m.list.Items()[0].(sessionItem).s.NativeID; first != "a" || m.list.Title != "aisle" {
		t.Errorf("after answering: first %q, title %q", first, m.list.Title)
	}
}

func TestNewTmuxSessionsAppear(t *testing.T) {
	m := newModel(snapWith("idle"))
	m.list.Select(2) // "c"
	m.applySnapshot(snapWith("idle", session.Runtime{Name: "new", Engine: "codex"}))
	if got := shownKeys(m); got != "tmux:new claude:a claude:b claude:c" {
		t.Errorf("items %s", got)
	}
	if sel := itemKey(m.list.SelectedItem()); sel != "claude:c" {
		t.Errorf("cursor on %s", sel)
	}
	m.applySnapshot(snapWith("idle")) // the tmux session ended
	if got := shownKeys(m); got != "claude:a claude:b claude:c" {
		t.Errorf("items %s", got)
	}
}

// Nothing moves under the user while they filter or read search results;
// the data is used once they are back.
func TestNoRedrawWhileFilteringOrInResults(t *testing.T) {
	m := newModel(snapWith("idle"))
	m.list.SetFilterText("claude")
	m.applySnapshot(snapWith("asking", session.Runtime{Name: "new", Engine: "codex"}))
	if strings.Contains(shownKeys(m), "tmux:new") {
		t.Error("redrawn while filtering")
	}
	m.list.ResetFilter()

	m.showResults("q", nil)
	m.applySnapshot(snapWith("idle", session.Runtime{Name: "later", Engine: "codex"}))
	if strings.Contains(shownKeys(m), "tmux:later") {
		t.Error("results replaced by a refresh")
	}
	m.browse()
	if !strings.Contains(shownKeys(m), "tmux:later") {
		t.Errorf("going back does not show the new data: %s", shownKeys(m))
	}
}

// "needs you" must stay visible where true color is unavailable, e.g. in
// macOS Terminal.app; a plain #E06C75 downsamples to near-black there.
func TestNeedsYouIsVisibleWithout24BitColor(t *testing.T) {
	defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
	for profile, want := range map[termenv.Profile]string{termenv.ANSI256: "38;5;204", termenv.ANSI: "91"} {
		lipgloss.SetColorProfile(profile)
		if got := askStyle.Render("x"); !strings.Contains(got, want) {
			t.Errorf("profile %v: %q, want color %s", profile, got, want)
		}
	}
}

func TestTickRediscoversOnlyNowAndThen(t *testing.T) {
	var fulls []bool
	m := newModel(snapWith("idle"))
	m.opts.Refresh = func(_ context.Context, cur service.Snapshot, full bool) service.Snapshot {
		fulls = append(fulls, full)
		return cur
	}
	m.lastFull = time.Now()
	run := func() {
		next, cmd := m.Update(tickMsg{})
		m = next.(model)
		cmd()
	}
	run()
	m.lastFull = time.Now().Add(-rediscoverEvery)
	run()
	run()
	if fmt.Sprint(fulls) != "[false true false]" {
		t.Errorf("full rediscoveries: %v", fulls)
	}
}
