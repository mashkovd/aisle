// Package gemini discovers Gemini CLI conversations.
//
// Storage, per project directory ~/.gemini/tmp/<project>/ (named by project
// hash in older versions, by project name in newer ones):
//
//	chats/session-*.jsonl  current (0.46+): a header line, then messages and {"$set": …} patches
//	chats/session-*.json   legacy: one object {sessionId, lastUpdated, messages: […]}
//	logs.json              legacy prompt log; not resumable on its own
//	.project_root          absolute project path
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/config"
	"github.com/mashkovd/aisle/internal/session"
)

const (
	name         = "gemini"
	formatJSONL  = "gemini-chat-jsonl"
	formatJSON   = "gemini-chat-json-legacy"
	formatLogs   = "gemini-logs-json-legacy"
	contextBlock = "<session_context>"
)

type Adapter struct {
	opts  adapter.Options
	roots []string
}

// New takes projectRoots from config to resolve directories that have no .project_root.
func New(opts adapter.Options, projectRoots []string) *Adapter {
	a := &Adapter{opts: opts}
	for _, r := range projectRoots {
		a.roots = append(a.roots, config.Expand(r, opts.Home))
	}
	return a
}

func (a *Adapter) Name() string               { return name }
func (a *Adapter) Letter() string             { return "g" }
func (a *Adapter) Capabilities() adapter.Caps { return adapter.Caps{ResumeByID: true} }

func (a *Adapter) root() string { return filepath.Join(a.opts.Home, ".gemini", "tmp") }

func (a *Adapter) Detect(ctx context.Context, withVersion bool) adapter.Detection {
	bin, ver := adapter.DetectBinary(ctx, "gemini", withVersion)
	return adapter.Detection{Installed: bin != "", Binary: bin, Version: ver, Storage: []string{a.root()}}
}

// Gemini scopes resume to the project directory, so Dir matters.
func (a *Adapter) Resume(s session.Session) adapter.Command {
	return adapter.Command{Argv: []string{"gemini", "--resume", s.NativeID}, Dir: s.Project}
}

func (a *Adapter) New(dir string) adapter.Command {
	return adapter.Command{Argv: []string{"gemini"}, Dir: dir}
}

type message struct {
	Type           string          `json:"type"`
	Timestamp      string          `json:"timestamp"`
	Content        json.RawMessage `json:"content"`
	DisplayContent json.RawMessage `json:"displayContent"`
}

// chat is the shape shared by the legacy object and the JSONL header/patches.
type chat struct {
	SessionID   string    `json:"sessionId"`
	LastUpdated string    `json:"lastUpdated"`
	StartTime   string    `json:"startTime"`
	Kind        string    `json:"kind"`
	Messages    []message `json:"messages"`
}

func (a *Adapter) Discover(ctx context.Context) ([]session.Session, []session.Warning) {
	var (
		out   []session.Session
		warns []session.Warning
	)
	warn := func(path, msg string) {
		warns = append(warns, session.Warning{Adapter: name, Path: path, Message: msg})
	}
	dirs, err := os.ReadDir(a.root())
	if err != nil {
		if !os.IsNotExist(err) {
			warn(a.root(), err.Error())
		}
		return nil, nil
	}
	for _, d := range dirs {
		if ctx.Err() != nil {
			break
		}
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(a.root(), d.Name())
		project := a.project(dir, d.Name())
		seen := map[string]bool{}    // listed sessions
		chatIDs := map[string]bool{} // every session with a chat file

		files, _ := filepath.Glob(filepath.Join(dir, "chats", "session-*.json*"))
		for _, f := range files {
			var (
				c      chat
				format string
				perr   error
			)
			switch filepath.Ext(f) {
			case ".jsonl":
				c, perr = readJSONL(f)
				format = formatJSONL
			case ".json":
				c, perr = readJSON(f)
				format = formatJSON
			default:
				continue
			}
			if perr != nil {
				warn(f, perr.Error())
				continue
			}
			chatIDs[c.SessionID] = true
			if c.Kind != "" && c.Kind != "main" {
				continue // subagent transcript
			}
			s, ok := a.toSession(c, project, session.Source{Kind: "conversation", Path: f, Format: format})
			if !ok {
				continue
			}
			if seen[s.NativeID] {
				continue
			}
			seen[s.NativeID] = true
			out = append(out, s)
		}

		logs := filepath.Join(dir, "logs.json")
		if n, err := orphanedLogSessions(logs, chatIDs); err != nil {
			warn(logs, err.Error())
		} else if n > 0 {
			warns = append(warns, session.Warning{
				Adapter: name, Path: logs, Info: true,
				Message: fmt.Sprintf("%d session(s) exist only in the legacy prompt log and cannot be resumed", n),
			})
		}
	}
	return out, warns
}

func (a *Adapter) project(dir, base string) string {
	if b, err := os.ReadFile(filepath.Join(dir, ".project_root")); err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			return p
		}
	}
	for _, r := range a.roots {
		if p := filepath.Join(r, base); isDir(p) {
			return p
		}
	}
	return ""
}

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func (a *Adapter) toSession(c chat, project string, src session.Source) (session.Session, bool) {
	if c.SessionID == "" {
		return session.Session{}, false
	}
	updated := parseTime(c.LastUpdated)
	var prompt string
	for _, m := range c.Messages {
		if t := parseTime(m.Timestamp); t.After(updated) {
			updated = t
		}
		if m.Type != "user" || prompt != "" {
			continue
		}
		text := textOf(m.DisplayContent)
		if text == "" {
			text = textOf(m.Content)
		}
		if strings.HasPrefix(strings.TrimSpace(text), contextBlock) || a.opts.Ignored(text) {
			continue
		}
		prompt = text
	}
	if prompt == "" {
		return session.Session{}, false // nothing was ever asked: auth-only or empty session
	}
	if updated.IsZero() {
		updated = parseTime(c.StartTime)
	}
	return session.Session{
		Engine:    name,
		NativeID:  c.SessionID,
		Project:   project,
		Summary:   session.Summarize(prompt, a.opts.SummaryLen),
		UpdatedAt: updated,
		State:     session.Historical,
		Sources:   []session.Source{src},
	}, true
}

// textOf accepts either a plain string or a list of {text} parts.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
			b.WriteByte(' ')
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func readJSON(path string) (chat, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return chat{}, err
	}
	var c chat
	if err := json.Unmarshal(b, &c); err != nil {
		return chat{}, fmt.Errorf("unsupported format: %w", err)
	}
	if c.SessionID == "" {
		return chat{}, fmt.Errorf("unsupported format: no sessionId")
	}
	return c, nil
}

// readJSONL replays the header, appended messages and $set patches.
func readJSONL(path string) (chat, error) {
	var (
		c     chat
		first = true
		bad   int
	)
	err := adapter.ScanAll(path, func(line []byte) {
		if first {
			first = false
			if !adapter.Decode(line, &c) || c.SessionID == "" {
				bad++
			}
			return
		}
		var probe struct {
			Set *chat `json:"$set"`
		}
		if adapter.Decode(line, &probe) && probe.Set != nil {
			if probe.Set.Messages != nil {
				c.Messages = probe.Set.Messages
			}
			if probe.Set.LastUpdated != "" {
				c.LastUpdated = probe.Set.LastUpdated
			}
			return
		}
		var m message
		if !adapter.Decode(line, &m) {
			bad++
			return
		}
		if m.Type != "" {
			c.Messages = append(c.Messages, m)
		}
	})
	if err != nil {
		return chat{}, err
	}
	if c.SessionID == "" {
		return chat{}, fmt.Errorf("unsupported format: header has no sessionId")
	}
	if bad > 0 {
		return chat{}, fmt.Errorf("unsupported format: %d unparseable line(s)", bad)
	}
	return c, nil
}

// orphanedLogSessions counts logs.json sessions without a chat file.
func orphanedLogSessions(path string, withChat map[string]bool) (int, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var entries []struct {
		SessionID string `json:"sessionId"`
		Type      string `json:"type"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		return 0, fmt.Errorf("unsupported format: %w", err)
	}
	orphans := map[string]bool{}
	for _, e := range entries {
		if e.SessionID != "" && !withChat[e.SessionID] && e.Type == "user" && !strings.HasPrefix(e.Message, "/") {
			orphans[e.SessionID] = true
		}
	}
	return len(orphans), nil
}
