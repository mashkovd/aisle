package codex

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

func (a *Adapter) Coverage() adapter.Coverage { return adapter.CoverageFull }

// Transcripts lists rollout files; the session ID is read from the first line
// because rollout file names are not guaranteed to end with it.
func (a *Adapter) Transcripts(ctx context.Context) ([]adapter.Transcript, []session.Warning) {
	var out []adapter.Transcript
	_ = filepath.WalkDir(filepath.Join(a.root(), "sessions"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		head, err := adapter.ReadHead(path, 64<<10)
		if err != nil || len(head.Raw) == 0 {
			return nil
		}
		var meta rolloutLine
		if !adapter.Decode(head.Raw[0], &meta) || meta.Type != "session_meta" || meta.Payload.ThreadSource == "subagent" {
			return nil
		}
		out = append(out, adapter.Transcript{Path: path, SessionID: meta.Payload.ID, Append: true})
		return nil
	})
	return out, nil
}

// Extract indexes user prompts (user_message events) and assistant replies
// (output_text); tool calls, reasoning and injected context are skipped.
func (a *Adapter) Extract(ctx context.Context, t adapter.Transcript, from int64, emit func(adapter.Message)) (int64, error) {
	return adapter.ScanFrom(ctx, t.Path, from, func(raw []byte) {
		var l rolloutLine
		if !adapter.Decode(raw, &l) {
			return
		}
		ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		switch {
		case l.Type == "event_msg" && l.Payload.Type == "user_message":
			if text := strings.TrimSpace(l.Payload.Message); text != "" {
				emit(adapter.Message{SessionID: t.SessionID, Role: "user", Text: adapter.Clip(text), Time: ts})
			}
		case l.Type == "response_item" && l.Payload.Type == "message" && l.Payload.Role == "assistant":
			for _, c := range l.Payload.Content {
				if text := strings.TrimSpace(c.Text); c.Type == "output_text" && text != "" {
					emit(adapter.Message{SessionID: t.SessionID, Role: "assistant", Text: adapter.Clip(text), Time: ts})
				}
			}
		}
	})
}
