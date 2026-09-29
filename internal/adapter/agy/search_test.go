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

// pb builds protobuf wire bytes: ints become varint fields, strings and
// []byte length-delimited ones.
func pb(kv ...any) []byte {
	var out []byte
	put := func(v uint64) {
		for v >= 0x80 {
			out = append(out, byte(v)|0x80)
			v >>= 7
		}
		out = append(out, byte(v))
	}
	for i := 0; i < len(kv); i += 2 {
		f := uint64(kv[i].(int))
		switch v := kv[i+1].(type) {
		case int:
			put(f << 3)
			put(uint64(v))
		case string:
			put(f<<3 | 2)
			put(uint64(len(v)))
			out = append(out, v...)
		case []byte:
			put(f<<3 | 2)
			put(uint64(len(v)))
			out = append(out, v...)
		}
	}
	return out
}

// conversation writes a conversation database with the steps table of agy
// 1.2 (schema copied verbatim) and the payload shapes seen in real ones.
func conversation(t *testing.T, home, id string, steps [][3]any) string {
	t.Helper()
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../../../testdata/agy/1.2/conversation-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		meta := pb(1, pb(1, s[2].(int), 2, 5000))
		if _, err := db.Exec(`INSERT INTO steps (idx, step_type, metadata, step_payload) VALUES (?, ?, ?, ?)`, i, s[0], meta, s[1]); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

const t0 = 1790000000

func steps() [][3]any {
	env := pb(12, "5e1b2c3d-0000-4000-8000-000000000001", 20, pb(1, "00000000-0000-4000-8000-00000000000a"))
	return [][3]any{
		{14, pb(5, env, 19, pb(2, "Why does the deploy lose its cookie?", 3, pb(1, "Why does the deploy lose its cookie?"))), t0},
		// thinking and a tool call, no visible text yet
		{15, pb(5, env, 20, pb(3, "Thinking about SameSite rules first.", 6, "bot-1", 7, pb(1, "list_dir"))), t0 + 1},
		{132, pb(5, env, 140, pb(2, "tool output: index.html")), t0 + 2},
		{15, pb(5, env, 20, pb(1, "The cookie is set with SameSite=Strict, so the redirect drops it.", 6, "bot-2", 8, "The cookie is set with SameSite=Strict, so the redirect drops it.")), t0 + 3},
		{14, pb(5, env, 19, pb(2, "/model"))}, // slash commands are not searchable
	}
}

func TestExtractConversation(t *testing.T) {
	home := t.TempDir()
	st := steps()
	st[4][2] = t0 + 4
	id := "0a1b2c3d-0000-4000-8000-000000000042"
	p := conversation(t, home, id, st)
	a := New(adapter.Options{Home: home})

	ts, _ := a.Transcripts(context.Background())
	var tr adapter.Transcript
	for _, x := range ts {
		if x.Path == p {
			tr = x
		}
	}
	if tr.SessionID != id || len(tr.Companions) != 1 || tr.Companions[0] != p+"-wal" || tr.Append {
		t.Fatalf("transcript %+v", tr)
	}
	var got []adapter.Message
	if _, err := a.Extract(context.Background(), tr, 0, func(m adapter.Message) { got = append(got, m) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages: %+v", len(got), got)
	}
	if got[0].Role != "user" || !strings.Contains(got[0].Text, "lose its cookie") || !got[0].Time.Equal(time.Unix(t0, 0)) {
		t.Errorf("prompt %+v", got[0])
	}
	if got[1].Role != "assistant" || !strings.Contains(got[1].Text, "SameSite=Strict") || got[1].SessionID != id {
		t.Errorf("reply %+v", got[1])
	}
	for _, m := range got {
		if strings.Contains(m.Text, "Thinking") || strings.Contains(m.Text, "tool output") || strings.Contains(m.Text, "bot-") {
			t.Errorf("indexed hidden text: %q", m.Text)
		}
	}
}

func TestExtractReportsUnreadableSteps(t *testing.T) {
	home := t.TempDir()
	st := steps()
	st[4][2] = t0 + 4
	st[3][1] = []byte{0xa2, 0x01, 0xff} // field 20, length beyond the end
	p := conversation(t, home, "bad", st)
	a := New(adapter.Options{Home: home})
	_, err := a.Extract(context.Background(), adapter.Transcript{Path: p, SessionID: "bad"}, 0, func(adapter.Message) {})
	if err == nil || !strings.Contains(err.Error(), "unsupported format: step 3") {
		t.Errorf("err = %v", err)
	}
}

func TestHistorySkipsConversationsWithADatabase(t *testing.T) {
	home := fixture(t, "1.2")
	b, err := os.ReadFile("../../../testdata/agy/1.2/history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(adapter.Options{Home: home})
	hist := adapter.Transcript{Path: a.historyPath(), Append: true}
	count := func() map[string]int {
		n := map[string]int{}
		if _, err := a.Extract(context.Background(), hist, 0, func(m adapter.Message) { n[m.SessionID]++ }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	if len(before) == 0 {
		t.Fatal("fixture history has no prompts")
	}
	var id string
	for k := range before {
		id = k
		break
	}
	st := steps()
	st[4][2] = t0 + 4
	conversation(t, home, id, st)
	if after := count(); after[id] != 0 || len(after) != len(before)-1 {
		t.Errorf("prompts of %s still read from history: %v", id, after)
	}
}
