package gemini

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
)

func discover(t *testing.T, fixture string, roots ...string) (*Adapter, []string, map[string]string, []string) {
	t.Helper()
	home, _ := filepath.Abs("../../../testdata/gemini/" + fixture + "/home")
	a := New(adapter.Options{Home: home, IgnorePrompts: map[string]bool{}, SummaryLen: 80}, roots)
	ss, ws := a.Discover(context.Background())
	var ids []string
	summaries := map[string]string{}
	for _, s := range ss {
		ids = append(ids, s.NativeID)
		summaries[s.NativeID] = s.Summary + "|" + s.Project + "|" + s.UpdatedAt.Format(time.RFC3339) + "|" + s.Sources[0].Format
	}
	var warns []string
	for _, w := range ws {
		warns = append(warns, w.Message)
	}
	return a, ids, summaries, warns
}

// 0.46.x: header + $set patches + appended messages; session_context and
// auth-only sessions are not conversations.
func TestGolden_0_46(t *testing.T) {
	_, ids, got, warns := discover(t, "0.46")
	if len(ids) != 1 || len(warns) != 0 {
		t.Fatalf("ids=%v warns=%v", ids, warns)
	}
	want := "explain the deploy pipeline|/work/demo|2026-08-06T16:27:00Z|gemini-chat-jsonl"
	if g := got["bbbb0001-0000-4000-8000-000000000001"]; g != want {
		t.Fatalf("got %q\nwant %q", g, want)
	}
}

func TestGolden_LegacyJSON(t *testing.T) {
	root := t.TempDir()
	_, ids, got, warns := discover(t, "legacy-json", root)
	if len(ids) != 1 {
		t.Fatalf("ids=%v", ids)
	}
	// "/model" is skipped as a summary; no .project_root and no matching root → unknown project
	want := "rotate the staging certificate||2026-04-08T16:44:25Z|gemini-chat-json-legacy"
	if g := got["bbbb0003-0000-4000-8000-000000000003"]; g != want {
		t.Fatalf("got %q\nwant %q", g, want)
	}
	// the logs.json-only session is reported, not dropped silently
	if len(warns) != 1 || !strings.Contains(warns[0], "1 session(s) exist only in the legacy prompt log") {
		t.Fatalf("warns=%v", warns)
	}
}

func TestBrokenFileIsReported(t *testing.T) {
	_, ids, _, warns := discover(t, "broken")
	if len(ids) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "unsupported format") {
		t.Fatalf("ids=%v warns=%v", ids, warns)
	}
}

func TestResumeUsesFullUUID(t *testing.T) {
	a, _, _, _ := discover(t, "0.46")
	home, _ := filepath.Abs("../../../testdata/gemini/0.46/home")
	ss, _ := New(adapter.Options{Home: home, IgnorePrompts: map[string]bool{}}, nil).Discover(context.Background())
	c := a.Resume(ss[0])
	if strings.Join(c.Argv, " ") != "gemini --resume bbbb0001-0000-4000-8000-000000000001" || c.Dir != "/work/demo" {
		t.Fatalf("got %+v", c)
	}
}
