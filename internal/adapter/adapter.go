// Package adapter defines the contract between aisle and one agent CLI.
// Adapters only discover history and describe how to start a runtime; they
// never run the agent themselves.
package adapter

import (
	"context"
	"os/exec"
	"strings"
	"time"

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

func (o Options) Ignored(prompt string) bool {
	p := strings.ToLower(strings.TrimSpace(prompt))
	return p == "" || o.IgnorePrompts[p] || strings.HasPrefix(p, "!")
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
