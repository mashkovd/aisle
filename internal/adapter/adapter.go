// Package adapter defines the contract between aisle and one agent CLI.
// Adapters only discover history and describe how to start a runtime; they
// never run the agent themselves.
package adapter

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mashkovd/aisle/internal/session"
)

// Command is what a runtime should execute: argv in Dir.
type Command struct {
	Argv []string
	Dir  string
}

type Detection struct {
	Installed bool     `json:"installed"`
	Binary    string   `json:"binary,omitempty"`
	Version   string   `json:"version,omitempty"`
	Storage   []string `json:"storage"` // storage paths the adapter reads, existing or not
}

type Caps struct {
	ResumeByID bool `json:"resume_by_id"`
}

type Adapter interface {
	Name() string
	// Letter is the one-character shortcut prefix used in the UI (c1, g2…).
	Letter() string
	Detect(ctx context.Context, withVersion bool) Detection
	Discover(ctx context.Context) ([]session.Session, []session.Warning)
	Resume(s session.Session) Command
	New(dir string) Command
	Capabilities() Caps
}

// Options are shared discovery settings coming from config.
type Options struct {
	Home          string          // user home, injectable for tests
	IgnorePrompts map[string]bool // lowercased prompts too trivial to use as a summary
	SummaryLen    int
}

// Ignored reports prompts that make a poor summary: configured trivial
// replies, slash commands and shell escapes.
func (o Options) Ignored(prompt string) bool {
	p := strings.ToLower(strings.TrimSpace(prompt))
	return p == "" || o.IgnorePrompts[p] || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "!")
}

// DetectBinary resolves name on PATH and, if asked, runs `name --version`.
func DetectBinary(ctx context.Context, name string, withVersion bool) (path, version string) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ""
	}
	if !withVersion {
		return path, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return path, "unknown"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return path, strings.TrimSpace(line)
}

// Transcript is one on-disk file holding conversation text.
type Transcript struct {
	Path      string
	SessionID string // default session for messages that do not carry one
	// Append marks an append-only file: indexing can resume from the last
	// offset instead of re-reading the whole file.
	Append bool
	// Companions are files whose changes also change this transcript, such
	// as a SQLite write-ahead log.
	Companions []string
}

// Message is one piece of conversation text worth searching.
type Message struct {
	SessionID string
	Role      string // user | assistant
	Text      string
	Time      time.Time
}

// Coverage describes how much conversation text an adapter can index.
type Coverage string

const (
	CoverageFull    Coverage = "full"    // prompts and replies
	CoveragePrompts Coverage = "prompts" // prompts only
)

// Searchable is implemented by adapters that can feed the full-text index.
type Searchable interface {
	Coverage() Coverage
	Transcripts(ctx context.Context) ([]Transcript, []session.Warning)
	// Extract emits messages from t starting at byte offset from (0 for
	// non-append files) and returns the offset just past the last complete
	// record it consumed.
	Extract(ctx context.Context, t Transcript, from int64, emit func(Message)) (int64, error)
}

// maxMessageText caps what is indexed per message; pasted logs can be huge.
const maxMessageText = 16 << 10

// Clip trims text to the per-message index cap on a rune boundary.
func Clip(text string) string {
	if len(text) <= maxMessageText {
		return text
	}
	cut := maxMessageText
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// ScanFrom calls fn for each complete line of path starting at offset from,
// and returns the offset just past the last complete line. A trailing line
// without a newline is left for the next call (it may still be written).
func ScanFrom(ctx context.Context, path string, from int64, fn func(line []byte)) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return from, err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return from, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	off := from
	for {
		if ctx.Err() != nil {
			return off, ctx.Err()
		}
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			return off, nil // partial or empty tail: not consumed
		}
		if err != nil {
			return off, err
		}
		off += int64(len(line))
		if l := bytes.TrimSpace(line); len(l) > 0 {
			fn(l)
		}
	}
}

// ArgvReader recognises the agent's own resume command in a process's
// arguments, so a tmux session aisle did not start can be linked to its
// conversation when — and only when — the command names it.
type ArgvReader interface {
	SessionFromArgv(argv []string) (id string, ok bool)
}

// IDAssigner starts a new conversation under an ID aisle picks, so the
// tmux session can be labelled with it before the agent writes anything.
type IDAssigner interface {
	NewWithID(dir string) (cmd Command, id string)
}

// AfterBinary returns the arguments that follow the first element of argv
// whose base name is bin — skipping wrappers such as `node` or `script`.
func AfterBinary(argv []string, bin string) ([]string, bool) {
	for i, a := range argv {
		if filepath.Base(a) == bin {
			return argv[i+1:], true
		}
	}
	return nil, false
}

// FlagValue returns the value of the first of names in args, written as
// `--name value` or `--name=value`.
func FlagValue(args []string, names ...string) (string, bool) {
	for i, a := range args {
		for _, n := range names {
			if a == n && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1], true
			}
			if v, ok := strings.CutPrefix(a, n+"="); ok && v != "" {
				return v, true
			}
		}
	}
	return "", false
}
