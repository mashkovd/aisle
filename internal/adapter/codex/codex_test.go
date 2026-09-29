package codex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mashkovd/aisle/internal/session"

	"github.com/mashkovd/aisle/internal/adapter"
)

func TestGolden_0_155(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home, _ := filepath.Abs("../../../testdata/codex/0.155/home")
	a := New(adapter.Options{Home: home, IgnorePrompts: map[string]bool{"ok": true}, SummaryLen: 80})
	ss, ws := a.Discover(context.Background())

	got := map[string]string{}
	for _, s := range ss {
		got[s.NativeID] = s.Summary + "|" + s.Project
	}
	want := map[string]string{
		// latest index entry wins over the first prompt
		"aaaa0001-0000-7000-8000-000000000001": "Audit auth middleware|/work/api",
		// no index entry: first non-trivial user_message ("ok" is ignored)
		"aaaa0003-0000-7000-8000-000000000003": "add dark mode|/work/web",
	}
	if len(got) != len(want) {
		t.Fatalf("subagent threads must be skipped; got %v", got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %q want %q", id, got[id], w)
		}
	}
	if len(ws) != 1 || !strings.Contains(ws[0].Message, "unsupported format") {
		t.Fatalf("unknown rollout must be reported: %v", ws)
	}
	for _, s := range ss {
		if s.NativeID == "aaaa0001-0000-7000-8000-000000000001" {
			c := a.Resume(s)
			if strings.Join(c.Argv, " ") != "codex resume "+s.NativeID || c.Dir != "/work/api" {
				t.Errorf("resume = %+v", c)
			}
		}
	}
}

func TestArgvRoundTrip(t *testing.T) {
	a := New(adapter.Options{})
	s := session.Session{Engine: "codex", NativeID: "abc-123"}
	if id, ok := a.SessionFromArgv(a.Resume(s).Argv); !ok || id != s.NativeID {
		t.Errorf("resume argv: %q %v", id, ok)
	}
	if id, ok := a.SessionFromArgv(a.New("/tmp").Argv); ok {
		t.Errorf("new argv names a session: %q", id)
	}
}
