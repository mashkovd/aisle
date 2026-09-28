package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

func (a *Adapter) Coverage() adapter.Coverage { return adapter.CoverageFull }

func (a *Adapter) Transcripts(ctx context.Context) ([]adapter.Transcript, []session.Warning) {
	files, _ := filepath.Glob(filepath.Join(a.root(), "projects", "*", "*.jsonl"))
	out := make([]adapter.Transcript, 0, len(files))
	for _, f := range files {
		out = append(out, adapter.Transcript{Path: f, SessionID: strings.TrimSuffix(filepath.Base(f), ".jsonl"), Append: true})
	}
	return out, nil
}

type msgLine struct {
	Type      string `json:"type"`
	IsMeta    bool   `json:"isMeta"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Harness-injected user turns that are not something the user typed.
var injected = []string{"<command-", "<local-command", "<system-reminder", "<bash-", "<task-notification", "Caveat:"}

// Extract indexes what the user typed and the assistant's visible replies;
// tool calls, tool output and thinking are skipped.
func (a *Adapter) Extract(ctx context.Context, t adapter.Transcript, from int64, emit func(adapter.Message)) (int64, error) {
	return adapter.ScanFrom(ctx, t.Path, from, func(raw []byte) {
		// cheap pre-filter: most lines are snapshots, progress and metadata;
		// tolerant of whitespace so it never hides a real message
		if !bytes.Contains(raw, []byte(`"user"`)) && !bytes.Contains(raw, []byte(`"assistant"`)) {
			return
		}
		var l msgLine
		if !adapter.Decode(raw, &l) {
			return
		}
		if (l.Type != "user" && l.Type != "assistant") || l.IsMeta {
			return
		}
		ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		var parts []string
		var s string
		if json.Unmarshal(l.Message.Content, &s) == nil {
			parts = append(parts, s)
		} else {
			var blocks []block
			if json.Unmarshal(l.Message.Content, &blocks) != nil {
				return
			}
			for _, b := range blocks {
				if b.Type == "text" {
					parts = append(parts, b.Text)
				}
			}
		}
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" || hasAnyPrefix(p, injected) {
				continue
			}
			emit(adapter.Message{SessionID: t.SessionID, Role: l.Type, Text: adapter.Clip(p), Time: ts})
		}
	})
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
