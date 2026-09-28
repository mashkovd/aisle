package codex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mashkovd/aisle/internal/adapter"
)

func TestExtract_0_155(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home, _ := filepath.Abs("../../../testdata/codex/0.155/home")
	a := New(adapter.Options{Home: home})
	ts, _ := a.Transcripts(context.Background())
	if len(ts) != 2 { // subagent and unknown rollouts are not transcripts
		t.Fatalf("transcripts: %+v", ts)
	}
	got := map[string][]string{}
	for _, tr := range ts {
		if _, err := a.Extract(context.Background(), tr, 0, func(m adapter.Message) {
			got[m.SessionID] = append(got[m.SessionID], m.Role+": "+m.Text)
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := "user: audit the auth middleware\nassistant: The middleware trusts X-Forwarded-For without a proxy allowlist."
	if g := strings.Join(got["aaaa0001-0000-7000-8000-000000000001"], "\n"); g != want {
		t.Fatalf("got:\n%s\nwant:\n%s", g, want)
	}
	for _, msgs := range got {
		for _, m := range msgs {
			if strings.Contains(m, "environment_context") || strings.Contains(m, "reasoning") || strings.Contains(m, "rg middleware") {
				t.Errorf("indexed injected or tool content: %q", m)
			}
		}
	}
}
