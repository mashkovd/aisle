package agy

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

// Coverage is prompts and replies, read from each conversation's own
// database (see proto.go). Conversations whose database is gone locally
// still have their prompts in history.jsonl.
func (a *Adapter) Coverage() adapter.Coverage { return adapter.CoverageFull }

func (a *Adapter) historyPath() string {
	return filepath.Join(a.opts.Home, ".gemini", "antigravity-cli", "history.jsonl")
}

func (a *Adapter) conversationsDir() string {
	return filepath.Join(a.opts.Home, ".gemini", "antigravity-cli", "conversations")
}

func (a *Adapter) Transcripts(ctx context.Context) ([]adapter.Transcript, []session.Warning) {
	ts := []adapter.Transcript{{Path: a.historyPath(), Append: true}}
	dbs, _ := filepath.Glob(filepath.Join(a.conversationsDir(), "*.db"))
	for _, p := range dbs {
		ts = append(ts, adapter.Transcript{
			Path: p, SessionID: strings.TrimSuffix(filepath.Base(p), ".db"),
			// agy writes through a write-ahead log: new steps land there first
			Companions: []string{p + "-wal"},
		})
	}
	return ts, nil
}

func (a *Adapter) Extract(ctx context.Context, t adapter.Transcript, from int64, emit func(adapter.Message)) (int64, error) {
	if strings.HasSuffix(t.Path, ".db") {
		return 0, a.extractConversation(ctx, t, emit)
	}
	return adapter.ScanFrom(ctx, t.Path, from, func(raw []byte) {
		var e struct {
			Display        string `json:"display"`
			Timestamp      string `json:"timestamp"`
			ConversationID string `json:"conversationId"`
		}
		if !adapter.Decode(raw, &e) || e.ConversationID == "" {
			return
		}
		// the conversation's database has this prompt too, with the replies
		if _, err := os.Stat(filepath.Join(a.conversationsDir(), e.ConversationID+".db")); err == nil {
			return
		}
		text := strings.TrimSpace(e.Display)
		if text == "" || strings.HasPrefix(text, "/") {
			return
		}
		var ts time.Time
		if ms, err := strconv.ParseInt(e.Timestamp, 10, 64); err == nil {
			ts = time.UnixMilli(ms)
		}
		emit(adapter.Message{SessionID: e.ConversationID, Role: "user", Text: adapter.Clip(text), Time: ts})
	})
}

// extractConversation reads prompts and visible replies from one
// conversation database. A prompt or reply step it cannot decode fails the
// whole file, so a format change is reported instead of silently missed.
func (a *Adapter) extractConversation(ctx context.Context, t adapter.Transcript, emit func(adapter.Message)) error {
	dsn := (&url.URL{Scheme: "file", Path: t.Path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT idx, step_type, metadata, step_payload FROM steps
		WHERE step_type IN (?, ?) ORDER BY idx`, stepUserInput, stepModelResponse)
	if err != nil {
		return fmt.Errorf("unsupported format: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			idx, typ          int
			metadata, payload []byte
		)
		if err := rows.Scan(&idx, &typ, &metadata, &payload); err != nil {
			return fmt.Errorf("unsupported format: %w", err)
		}
		role, text, ok, err := stepText(typ, payload)
		if err != nil {
			return fmt.Errorf("unsupported format: step %d: %w", idx, err)
		}
		text = strings.TrimSpace(text)
		if !ok || text == "" || (role == "user" && strings.HasPrefix(text, "/")) {
			continue
		}
		emit(adapter.Message{SessionID: t.SessionID, Role: role, Text: adapter.Clip(text), Time: stepTime(metadata)})
	}
	return rows.Err()
}
