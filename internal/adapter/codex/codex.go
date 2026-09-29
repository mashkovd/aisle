// Package codex discovers OpenAI Codex CLI conversations.
//
// Storage:
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl  first line is session_meta {id, cwd, source, thread_source}
//	~/.codex/session_index.jsonl                  {id, thread_name, updated_at}, appended on change
package codex

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

const (
	name        = "codex"
	formatRoll  = "codex-rollout-jsonl"
	formatIndex = "codex-session-index-jsonl"
)

var headWindow int64 = 512 << 10

type Adapter struct{ opts adapter.Options }

func New(opts adapter.Options) *Adapter { return &Adapter{opts: opts} }

func (a *Adapter) Name() string               { return name }
func (a *Adapter) Letter() string             { return "x" }
func (a *Adapter) Capabilities() adapter.Caps { return adapter.Caps{ResumeByID: true} }

func (a *Adapter) root() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(a.opts.Home, ".codex")
}

func (a *Adapter) Detect(ctx context.Context, withVersion bool) adapter.Detection {
	bin, ver := adapter.DetectBinary(ctx, "codex", withVersion)
	return adapter.Detection{
		Installed: bin != "", Binary: bin, Version: ver,
		Storage: []string{filepath.Join(a.root(), "sessions"), filepath.Join(a.root(), "session_index.jsonl")},
	}
}

func (a *Adapter) Resume(s session.Session) adapter.Command {
	return adapter.Command{Argv: []string{"codex", "resume", s.NativeID}, Dir: s.Project}
}

func (a *Adapter) New(dir string) adapter.Command {
	return adapter.Command{Argv: []string{"codex"}, Dir: dir}
}

// SessionFromArgv reads `codex [flags] resume <id>`.
func (a *Adapter) SessionFromArgv(argv []string) (string, bool) {
	args, ok := adapter.AfterBinary(argv, "codex")
	if !ok {
		return "", false
	}
	for i, x := range args {
		if x == "resume" {
			for _, y := range args[i+1:] {
				if !strings.HasPrefix(y, "-") {
					return y, true
				}
			}
			return "", false
		}
	}
	return "", false
}

type indexEntry struct {
	name    string
	updated time.Time
}

type rolloutLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		ID           string `json:"id"`
		Cwd          string `json:"cwd"`
		Type         string `json:"type"`
		Role         string `json:"role"`
		Message      string `json:"message"`
		ThreadSource string `json:"thread_source"`
		Content      []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

func (a *Adapter) Discover(ctx context.Context) ([]session.Session, []session.Warning) {
	var warns []session.Warning
	warn := func(path, msg string) {
		warns = append(warns, session.Warning{Adapter: name, Path: path, Message: msg})
	}

	indexPath := filepath.Join(a.root(), "session_index.jsonl")
	index := map[string]indexEntry{}
	if err := adapter.ScanAll(indexPath, func(line []byte) {
		var e struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
			UpdatedAt  string `json:"updated_at"`
		}
		if adapter.Decode(line, &e) && e.ID != "" {
			t, _ := time.Parse(time.RFC3339Nano, e.UpdatedAt)
			index[e.ID] = indexEntry{name: e.ThreadName, updated: t} // later lines win
		}
	}); err != nil && !os.IsNotExist(err) {
		warn(indexPath, err.Error())
	}

	var out []session.Session
	sessionsDir := filepath.Join(a.root(), "sessions")
	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		s, ok, msg := a.readRollout(path, index)
		if msg != "" {
			warn(path, msg)
		}
		if ok {
			if _, has := index[s.NativeID]; has {
				s.Sources = append(s.Sources, session.Source{Kind: "metadata", Path: indexPath, Format: formatIndex})
			}
			out = append(out, s)
		}
		return nil
	})
	return out, warns
}

func (a *Adapter) readRollout(path string, index map[string]indexEntry) (session.Session, bool, string) {
	head, err := adapter.ReadHead(path, headWindow)
	if err != nil {
		return session.Session{}, false, err.Error()
	}
	if len(head.Raw) == 0 {
		return session.Session{}, false, ""
	}
	var meta rolloutLine
	if !adapter.Decode(head.Raw[0], &meta) || meta.Type != "session_meta" || meta.Payload.ID == "" {
		return session.Session{}, false, "unsupported format: first line is not session_meta"
	}
	if meta.Payload.ThreadSource == "subagent" {
		return session.Session{}, false, ""
	}
	var prompt string
	for _, raw := range head.Raw[1:] {
		var l rolloutLine
		if !adapter.Decode(raw, &l) {
			continue
		}
		switch {
		case l.Type == "event_msg" && l.Payload.Type == "user_message":
			prompt = l.Payload.Message
		case l.Type == "response_item" && l.Payload.Type == "message" && l.Payload.Role == "user":
			for _, c := range l.Payload.Content {
				if t := strings.TrimSpace(c.Text); t != "" && !strings.HasPrefix(t, "<") {
					prompt = t
					break
				}
			}
		}
		if prompt != "" && !a.opts.Ignored(prompt) {
			break
		}
		prompt = ""
	}

	id := meta.Payload.ID
	e := index[id]
	updated := e.updated
	if st, err := os.Stat(path); err == nil && st.ModTime().After(updated) {
		updated = st.ModTime()
	}
	label := e.name
	if strings.TrimSpace(label) == "" {
		label = prompt
	}
	return session.Session{
		Engine:    name,
		NativeID:  id,
		Project:   meta.Payload.Cwd,
		Summary:   session.Summarize(label, a.opts.SummaryLen),
		UpdatedAt: updated,
		State:     session.Historical,
		Sources:   []session.Source{{Kind: "conversation", Path: path, Format: formatRoll}},
	}, true, ""
}
