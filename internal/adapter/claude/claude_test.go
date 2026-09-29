package claude

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

func opts(home string) adapter.Options {
	return adapter.Options{Home: home, IgnorePrompts: map[string]bool{"да": true}, SummaryLen: 80}
}

func byID(t *testing.T, ss []session.Session) map[string]session.Session {
	t.Helper()
	m := map[string]session.Session{}
	for _, s := range ss {
		m[s.NativeID] = s
	}
	return m
}

func TestDiscoverGolden_2_1(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := filepath.Abs("../../../testdata/claude/2.1/home")
	ss, warns := New(opts(home)).Discover(context.Background())
	got := byID(t, ss)

	if len(ss) != 2 {
		t.Fatalf("want 2 sessions (empty and unknown files skipped), got %d: %+v", len(ss), ss)
	}
	a := got["11111111-aaaa-4bbb-8ccc-000000000001"]
	if a.Summary != "Fix flaky login test" {
		t.Errorf("ai-title should win over prompts, got %q", a.Summary)
	}
	if a.Project != "/work/demo" {
		t.Errorf("project = %q", a.Project)
	}
	if want := time.Date(2026, 9, 1, 11, 30, 0, 0, time.UTC); !a.UpdatedAt.Equal(want) {
		t.Errorf("updated = %v, want %v", a.UpdatedAt, want)
	}
	if len(a.Sources) != 2 || a.Sources[1].Format != formatHist {
		t.Errorf("history.jsonl should be recorded as a metadata source: %+v", a.Sources)
	}
	if b := got["22222222-aaaa-4bbb-8ccc-000000000002"]; b.Summary != "Release 1.4 notes" {
		t.Errorf("custom-title should win, got %q", b.Summary)
	}

	// the file with no typed lines must be reported, not dropped silently
	var unsupported bool
	for _, w := range warns {
		if strings.Contains(w.Path, "44444444") && strings.Contains(w.Message, "unsupported format") {
			unsupported = true
		}
	}
	if !unsupported {
		t.Errorf("want an unsupported-format warning for the unknown file, got %v", warns)
	}
}

func TestResumeCommand(t *testing.T) {
	c := New(opts("/nonexistent")).Resume(session.Session{NativeID: "abc", Project: "/work/demo"})
	if strings.Join(c.Argv, " ") != "claude --resume abc" || c.Dir != "/work/demo" {
		t.Fatalf("got %+v", c)
	}
}

// Large conversations are read as head+tail windows; titles written late and
// the cwd written early must both survive.
func TestHeadTailWindows(t *testing.T) {
	oldH, oldT := headWindow, tailWindow
	headWindow, tailWindow = 512, 512
	defer func() { headWindow, tailWindow = oldH, oldT }()

	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-big")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"user","cwd":"/big","timestamp":"2026-01-01T00:00:00Z","sessionId":"big"}` + "\n")
	for i := 0; i < 200; i++ {
		b.WriteString(`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"content":"` + strings.Repeat("x", 40) + `"}}` + "\n")
	}
	b.WriteString(`{"type":"ai-title","aiTitle":"Late title"}` + "\n")
	b.WriteString(`{"type":"user","timestamp":"2026-02-01T00:00:00Z"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "big.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	ss, warns := New(opts(home)).Discover(context.Background())
	if len(ss) != 1 || len(warns) != 0 {
		t.Fatalf("sessions=%v warns=%v", ss, warns)
	}
	s := ss[0]
	if s.Project != "/big" || s.Summary != "Late title" || s.UpdatedAt.Month() != time.February {
		t.Fatalf("got %+v", s)
	}
}

func TestArgvRoundTrip(t *testing.T) {
	a := New(adapter.Options{})
	s := session.Session{Engine: "claude", NativeID: "0d71ed00-1111-4222-8333-444455556666"}
	if id, ok := a.SessionFromArgv(a.Resume(s).Argv); !ok || id != s.NativeID {
		t.Errorf("resume argv: %q %v", id, ok)
	}
	cmd, id := a.NewWithID("/tmp")
	if got, ok := a.SessionFromArgv(cmd.Argv); !ok || got != id {
		t.Errorf("new argv: %q %v", got, ok)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Errorf("not a v4 UUID: %s", id)
	}
	for _, argv := range [][]string{{"claude"}, {"claude", "-c"}, {"claude", "--resume"}, {"claude", "--resume", "--verbose"}, {"clauded", "--resume", "x"}} {
		if id, ok := a.SessionFromArgv(argv); ok {
			t.Errorf("%v: got %q", argv, id)
		}
	}
	if id, _ := a.SessionFromArgv([]string{"node", "/x/claude", "--resume=abc"}); id != "abc" {
		t.Errorf("--resume=: %q", id)
	}
}
