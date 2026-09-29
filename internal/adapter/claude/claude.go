// Package claude discovers Claude Code conversations.
//
// Storage (Claude Code 2.x):
//
//	~/.claude/projects/<slug>/<session-uuid>.jsonl  one conversation, append-only
//	~/.claude/history.jsonl                           every prompt: display, project, sessionId
//
// Conversation files grow to tens of MB, so only a head window (for cwd) and
// a tail window (for titles and last activity) are read.
package claude

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

const (
	name       = "claude"
	formatConv = "claude-jsonl-v1"
	formatHist = "claude-history-jsonl"
)

// read windows; variables so tests can force the head/tail split
var (
	headWindow int64 = 256 << 10
	tailWindow int64 = 1 << 20
)

type Adapter struct{ opts adapter.Options }

func New(opts adapter.Options) *Adapter { return &Adapter{opts: opts} }

func (a *Adapter) Name() string               { return name }
func (a *Adapter) Letter() string             { return "c" }
func (a *Adapter) Capabilities() adapter.Caps { return adapter.Caps{ResumeByID: true} }

func (a *Adapter) root() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(a.opts.Home, ".claude")
}

func (a *Adapter) Detect(ctx context.Context, withVersion bool) adapter.Detection {
	bin, ver := adapter.DetectBinary(ctx, "claude", withVersion)
	return adapter.Detection{
		Installed: bin != "", Binary: bin, Version: ver,
		Storage: []string{filepath.Join(a.root(), "projects"), filepath.Join(a.root(), "history.jsonl")},
	}
}

func (a *Adapter) Resume(s session.Session) adapter.Command {
	return adapter.Command{Argv: []string{"claude", "--resume", s.NativeID}, Dir: s.Project}
}

func (a *Adapter) New(dir string) adapter.Command {
	return adapter.Command{Argv: []string{"claude"}, Dir: dir}
}

// NewWithID starts the conversation under a fresh session ID, which Claude
// Code accepts with --session-id.
func (a *Adapter) NewWithID(dir string) (adapter.Command, string) {
	id := newUUID()
	return adapter.Command{Argv: []string{"claude", "--session-id", id}, Dir: dir}, id
}

func (a *Adapter) SessionFromArgv(argv []string) (string, bool) {
	args, ok := adapter.AfterBinary(argv, "claude")
	if !ok {
		return "", false
	}
	return adapter.FlagValue(args, "--resume", "-r", "--session-id")
}

// newUUID returns a random (version 4) UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type histEntry struct {
	project string
	prompt  string // latest non-trivial prompt
}

type line struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	AITitle     string `json:"aiTitle"`
	CustomTitle string `json:"customTitle"`
	LastPrompt  string `json:"lastPrompt"`
	Summary     string `json:"summary"`
}

func (a *Adapter) Discover(ctx context.Context) ([]session.Session, []session.Warning) {
	var warns []session.Warning
	warn := func(path, msg string) {
		warns = append(warns, session.Warning{Adapter: name, Path: path, Message: msg})
	}

	histPath := filepath.Join(a.root(), "history.jsonl")
	hist, err := a.readHistory(histPath)
	if err != nil && !os.IsNotExist(err) {
		warn(histPath, err.Error())
	}

	files, _ := filepath.Glob(filepath.Join(a.root(), "projects", "*", "*.jsonl"))
	var out []session.Session
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		s, ok, msg := a.readConversation(f, hist)
		if msg != "" {
			warn(f, msg)
		}
		if ok {
			if _, used := hist[s.NativeID]; used {
				s.Sources = append(s.Sources, session.Source{Kind: "metadata", Path: histPath, Format: formatHist})
			}
			out = append(out, s)
		}
	}
	return out, warns
}

func (a *Adapter) readHistory(path string) (map[string]histEntry, error) {
	hist := map[string]histEntry{}
	err := adapter.ScanAll(path, func(raw []byte) {
		var e struct {
			Display   string `json:"display"`
			Project   string `json:"project"`
			SessionID string `json:"sessionId"`
		}
		if !adapter.Decode(raw, &e) || e.SessionID == "" {
			return
		}
		h := hist[e.SessionID]
		if e.Project != "" {
			h.project = e.Project
		}
		if !a.opts.Ignored(e.Display) {
			h.prompt = e.Display // file is chronological: keep the latest
		}
		hist[e.SessionID] = h
	})
	return hist, err
}

// readConversation returns ok=false for files that hold no conversation; msg
// is a warning for anything that could not be read as expected.
func (a *Adapter) readConversation(path string, hist map[string]histEntry) (session.Session, bool, string) {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	st, err := os.Stat(path)
	if err != nil {
		return session.Session{}, false, err.Error()
	}
	head, err := adapter.ReadHead(path, headWindow)
	if err != nil {
		return session.Session{}, false, err.Error()
	}
	tail := head
	if head.Truncated {
		if tail, err = adapter.ReadTail(path, tailWindow); err != nil {
			return session.Session{}, false, err.Error()
		}
	}

	var (
		cwd, aiTitle, customTitle, lastPrompt, summary string
		updated                                        time.Time
		typed, untyped, bad, messages                  int
	)
	visit := func(lines [][]byte, fromTail bool) {
		for _, raw := range lines {
			var l line
			if !adapter.Decode(raw, &l) {
				bad++
				continue
			}
			if l.Type == "" {
				untyped++
				continue
			}
			typed++
			if cwd == "" && l.Cwd != "" {
				cwd = l.Cwd
			}
			switch l.Type {
			case "assistant":
				messages++
			case "ai-title":
				aiTitle = l.AITitle
			case "custom-title":
				customTitle = l.CustomTitle
			case "last-prompt":
				lastPrompt = l.LastPrompt
			case "summary":
				summary = l.Summary
			}
			if fromTail || !head.Truncated {
				if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil && t.After(updated) {
					updated = t
				}
			}
		}
	}
	visit(head.Raw, false)
	if head.Truncated {
		visit(tail.Raw, true)
	}

	if typed == 0 {
		if bad+untyped > 0 {
			return session.Session{}, false, "unsupported format: no line has a \"type\" field"
		}
		return session.Session{}, false, "" // empty file
	}
	if messages == 0 && aiTitle == "" && customTitle == "" {
		return session.Session{}, false, "" // no reply and no title: only local commands or snapshots
	}

	h := hist[id]
	project := cwd
	if project == "" {
		project = h.project
	}
	label := firstNonEmpty(customTitle, aiTitle, summary, h.prompt, lastPrompt)
	if updated.IsZero() {
		updated = st.ModTime()
	}
	var msg string
	if bad > 0 {
		msg = "some lines could not be parsed"
	}
	return session.Session{
		Engine:    name,
		NativeID:  id,
		Project:   project,
		Summary:   session.Summarize(label, a.opts.SummaryLen),
		UpdatedAt: updated,
		State:     session.Historical,
		Sources:   []session.Source{{Kind: "conversation", Path: path, Format: formatConv, Partial: bad > 0}},
	}, true, msg
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
