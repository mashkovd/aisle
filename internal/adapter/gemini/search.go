package gemini

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

func (a *Adapter) Coverage() adapter.Coverage { return adapter.CoverageFull }

// Transcripts lists chat files. They are not append-only ($set patches can
// rewrite the whole message list), so each change re-reads the file.
func (a *Adapter) Transcripts(ctx context.Context) ([]adapter.Transcript, []session.Warning) {
	files, _ := filepath.Glob(filepath.Join(a.root(), "*", "chats", "session-*.json*"))
	out := make([]adapter.Transcript, 0, len(files))
	for _, f := range files {
		if ext := filepath.Ext(f); ext == ".json" || ext == ".jsonl" {
			out = append(out, adapter.Transcript{Path: f})
		}
	}
	return out, nil
}

func (a *Adapter) Extract(ctx context.Context, t adapter.Transcript, _ int64, emit func(adapter.Message)) (int64, error) {
	st, err := os.Stat(t.Path)
	if err != nil {
		return 0, err
	}
	var c chat
	if filepath.Ext(t.Path) == ".jsonl" {
		c, err = readJSONL(t.Path)
	} else {
		c, err = readJSON(t.Path)
	}
	if err != nil {
		return 0, err
	}
	if c.Kind != "" && c.Kind != "main" {
		return st.Size(), nil
	}
	for _, m := range c.Messages {
		role := ""
		switch m.Type {
		case "user":
			role = "user"
		case "gemini":
			role = "assistant"
		default:
			continue
		}
		text := textOf(m.DisplayContent)
		if text == "" {
			text = textOf(m.Content)
		}
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, contextBlock) {
			continue
		}
		emit(adapter.Message{SessionID: c.SessionID, Role: role, Text: adapter.Clip(text), Time: parseTime(m.Timestamp)})
	}
	return st.Size(), nil
}
