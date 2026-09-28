package gemini

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mashkovd/aisle/internal/adapter"
)

func TestExtract_0_46_and_legacy(t *testing.T) {
	for _, c := range []struct{ fixture, want string }{
		{"0.46", "user: explain the deploy pipeline\nassistant: The pipeline has three stages."},
		{"legacy-json", "user: /model\nuser: rotate the staging certificate\nassistant: Done."},
	} {
		home, _ := filepath.Abs("../../../testdata/gemini/" + c.fixture + "/home")
		a := New(adapter.Options{Home: home}, nil)
		ts, _ := a.Transcripts(context.Background())
		var got []string
		for _, tr := range ts {
			if tr.Append {
				t.Fatalf("gemini chats are rewritten by $set patches; must not be append-only")
			}
			_, _ = a.Extract(context.Background(), tr, 0, func(m adapter.Message) {
				got = append(got, m.Role+": "+m.Text)
			})
		}
		if g := strings.Join(got, "\n"); g != c.want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", c.fixture, g, c.want)
		}
	}
}
