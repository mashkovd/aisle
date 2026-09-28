package agy

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

// Coverage is prompts only: agy keeps replies in undocumented protobuf blobs
// (conversations/<id>.db), which aisle does not parse.
func (a *Adapter) Coverage() adapter.Coverage { return adapter.CoveragePrompts }

func (a *Adapter) historyPath() string {
	return filepath.Join(a.opts.Home, ".gemini", "antigravity-cli", "history.jsonl")
}

func (a *Adapter) Transcripts(ctx context.Context) ([]adapter.Transcript, []session.Warning) {
	return []adapter.Transcript{{Path: a.historyPath(), Append: true}}, nil
}

func (a *Adapter) Extract(ctx context.Context, t adapter.Transcript, from int64, emit func(adapter.Message)) (int64, error) {
	return adapter.ScanFrom(ctx, t.Path, from, func(raw []byte) {
		var e struct {
			Display        string `json:"display"`
			Timestamp      string `json:"timestamp"`
			ConversationID string `json:"conversationId"`
		}
		if !adapter.Decode(raw, &e) || e.ConversationID == "" {
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
