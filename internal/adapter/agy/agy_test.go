package agy

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
)

// The schema is copied verbatim from agy 1.2; rows are synthetic.
func fixture(t *testing.T, version string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, f := range []string{"schema.sql", "rows.sql"} {
		b, err := os.ReadFile(filepath.Join("../../../testdata/agy", version, f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	return home
}

func TestGolden_1_2(t *testing.T) {
	a := New(adapter.Options{Home: fixture(t, "1.2"), SummaryLen: 80})
	ss, ws := a.Discover(context.Background())
	if len(ws) != 0 || len(ss) != 2 {
		t.Fatalf("subagent and unused rows must be skipped: %+v %v", ss, ws)
	}
	if s := ss[0]; s.Summary != "Compare infra costs" || s.Project != "/work/infra" ||
		!s.UpdatedAt.Equal(time.Date(2026, 9, 26, 13, 1, 37, 793712000, time.UTC)) {
		t.Errorf("first: %+v", s)
	}
	// empty title falls back to preview; the 0001-01-01 placeholder falls back to last input
	if s := ss[1]; s.Summary != "Describe the project" || s.Project != "/work/web app" || s.UpdatedAt.Day() != 25 {
		t.Errorf("second: %+v", s)
	}
	if c := a.Resume(ss[0]); strings.Join(c.Argv, " ") != "agy --conversation cccc0001-0000-4000-8000-000000000001" {
		t.Errorf("resume: %+v", c)
	}
}

func TestSchemaDriftIsReported(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	_ = os.MkdirAll(dir, 0o755)
	db, _ := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
	_, _ = db.Exec(`CREATE TABLE conversation_summaries (id text)`)
	db.Close()
	ss, ws := New(adapter.Options{Home: home}).Discover(context.Background())
	if len(ss) != 0 || len(ws) != 1 || !strings.Contains(ws[0].Message, "unsupported format") {
		t.Fatalf("got %v %v", ss, ws)
	}
}

func TestExtractPromptsOnly(t *testing.T) {
	home := fixture(t, "1.2")
	b, err := os.ReadFile("../../../testdata/agy/1.2/history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(adapter.Options{Home: home})
	if a.Coverage() != adapter.CoveragePrompts {
		t.Fatal("agy replies are not readable; coverage must say prompts only")
	}
	ts, _ := a.Transcripts(context.Background())
	var got []string
	for _, tr := range ts {
		_, _ = a.Extract(context.Background(), tr, 0, func(m adapter.Message) {
			got = append(got, m.SessionID[:8]+" "+m.Text)
		})
	}
	want := "cccc0001 compare infra costs across regions\ncccc0002 describe the web app"
	if g := strings.Join(got, "\n"); g != want {
		t.Fatalf("got:\n%s\nwant:\n%s", g, want)
	}
}
