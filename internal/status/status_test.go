package status

import (
	"os"
	"path/filepath"
	"testing"
)

// The screens are real captures of the agents in tmux (paths and account
// names replaced).
func screen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "status", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClassifyScreens(t *testing.T) {
	for _, c := range []struct {
		name, prev string
		want       State
	}{
		{"claude/asking", "", Asking},
		{"claude/idle", "", Idle},
		{"claude/done", "", Idle},
		{"claude/spinner", "", Working},
		{"claude/streaming", "", Idle},               // no marker on one look…
		{"claude/streaming", "claude/done", Working}, // …but it changed since the last
		{"codex/idle", "", Idle},
		{"codex/asking-menu", "", Asking},
		{"agy/asking-trust", "", Asking},
		{"agy/idle", "", Idle},
		{"agy/idle", "agy/idle", Idle}, // agy redraws the same screen: not work
		{"agy/working", "", Working},
		{"agy/asking", "", Asking},
		{"agy/done", "", Idle},
	} {
		prev := ""
		if c.prev != "" {
			prev = screen(t, c.prev)
		}
		if got := Classify(screen(t, c.name), prev, "claude"); got != c.want {
			t.Errorf("%s (prev %q): %q, want %q", c.name, c.prev, got, c.want)
		}
	}
}

func TestClassifyExited(t *testing.T) {
	for cmd, want := range map[string]State{"zsh": Exited, "-zsh": Exited, "/bin/bash": Exited, "claude": Idle, "node": Idle, "": Idle} {
		if got := Classify("❯ \n", "", cmd); got != want {
			t.Errorf("%q: %q, want %q", cmd, got, want)
		}
	}
}

func TestOnlyTheBottomCounts(t *testing.T) {
	// an old question scrolled far up is not an open one
	s := "Do you want to proceed?\n"
	for i := 0; i < bottom+2; i++ {
		s += "line\n"
	}
	if got := Classify(s, "", "claude"); got != Idle {
		t.Errorf("got %q", got)
	}
}

func TestTrailingBlanksAreNotChanges(t *testing.T) {
	if got := Classify("a\nb", "a   \nb\n\n", "claude"); got != Idle {
		t.Errorf("got %q", got)
	}
}
