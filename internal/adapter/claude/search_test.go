package claude

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mashkovd/aisle/internal/adapter"
)

func TestExtractKeepsOnlyConversationText(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := filepath.Abs("../../../testdata/claude/2.1/home")
	a := New(opts(home))
	ts, _ := a.Transcripts(context.Background())
	var tr adapter.Transcript
	for _, x := range ts {
		if x.SessionID == "11111111-aaaa-4bbb-8ccc-000000000001" {
			tr = x
		}
	}
	if !tr.Append {
		t.Fatalf("claude transcripts are append-only: %+v", tr)
	}
	var got []string
	end, err := a.Extract(context.Background(), tr, 0, func(m adapter.Message) {
		got = append(got, m.Role+": "+m.Text)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"user: fix the flaky login test",
		"assistant: Looking.",
		"assistant: The retry test races on the session cookie.",
		"user: run it again",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// thinking, tool calls/results and harness-injected turns never get indexed
	for _, g := range got {
		for _, bad := range []string{"chain of thought", "go test", "toolresultonly", "Caveat", "/clear"} {
			if strings.Contains(g, bad) {
				t.Errorf("indexed %q", g)
			}
		}
	}
	if end == 0 {
		t.Error("offset not advanced")
	}
}
