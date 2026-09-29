// Package rules checks that one AGENTS.md reaches every agent aisle knows,
// and plans the file edits that make it so.
//
// It never runs an agent. What it knows about how each agent loads its
// instruction files is encoded in the per-agent checkers and pinned by the
// golden cases in testdata/rules; behaviour that has not been confirmed
// against a real agent is reported with Unverified set.
package rules

import (
	"path/filepath"
	"strings"
)

type Level string

const (
	Info  Level = "info"
	Warn  Level = "warning"
	Error Level = "error"
)

// Reach says whether an agent ends up reading the source AGENTS.md.
type Reach string

const (
	Native   Reach = "native"   // the agent reads the file by itself
	Imported Reach = "import"   // another instruction file imports or symlinks it
	Config   Reach = "settings" // the agent is configured to read it (Gemini)
	Missing  Reach = "missing"
)

// Finding is one problem or note about a project's or the user's rules.
type Finding struct {
	Agent   string `json:"agent,omitempty"`
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
	// Unverified marks findings that rest on agent behaviour aisle has not
	// confirmed against a real agent.
	Unverified bool `json:"unverified,omitempty"`
	Fix        *Op  `json:"fix,omitempty"`
}

// AgentReport is what one agent loads.
type AgentReport struct {
	Agent string   `json:"agent"`
	Reach Reach    `json:"reach"`
	Via   string   `json:"via,omitempty"` // the file or setting that carries AGENTS.md
	Files []string `json:"files"`         // instruction files the agent loads here, in order
}

type Report struct {
	Scope    string        `json:"scope"` // project | global
	Root     string        `json:"root"`
	Source   string        `json:"source"` // the AGENTS.md every agent should read
	Exists   bool          `json:"source_exists"`
	Agents   []AgentReport `json:"agents"`
	Findings []Finding     `json:"findings"`
}

// Problems counts warnings and errors; info notes are not problems.
func (r Report) Problems() int {
	n := 0
	for _, f := range r.Findings {
		if f.Level != Info {
			n++
		}
	}
	return n
}

// Fixes returns the operations the findings propose, without duplicates.
func (r Report) Fixes() []Op {
	var ops []Op
	seen := map[string]bool{}
	for _, f := range r.Findings {
		if f.Fix == nil {
			continue
		}
		k := f.Fix.key()
		if !seen[k] {
			seen[k] = true
			ops = append(ops, *f.Fix)
		}
	}
	return ops
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

// Env is everything a check reads. Paths are absolute.
type Env struct {
	Home      string
	ClaudeDir string // ~/.claude or $CLAUDE_CONFIG_DIR
	CodexDir  string // ~/.codex or $CODEX_HOME
	GeminiDir string // ~/.gemini
	// ClaudeVersion is `claude --version` output; empty when unknown.
	ClaudeVersion string
}

// DefaultEnv derives agent directories from home and the agents' own
// environment variables.
func DefaultEnv(home string, getenv func(string) string) Env {
	e := Env{
		Home:      home,
		ClaudeDir: filepath.Join(home, ".claude"),
		CodexDir:  filepath.Join(home, ".codex"),
		GeminiDir: filepath.Join(home, ".gemini"),
	}
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		e.ClaudeDir = d
	}
	if d := getenv("CODEX_HOME"); d != "" {
		e.CodexDir = d
	}
	if d := getenv("GEMINI_CLI_HOME"); d != "" {
		e.GeminiDir = filepath.Join(d, ".gemini")
	}
	return e
}

// Display shortens p for output: relative to root when inside it, else ~/….
func Display(p, root, home string) string {
	if root != "" {
		if within(p, root) {
			rel, _ := filepath.Rel(root, p)
			return rel
		}
	}
	if home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + p[len(home):]
	}
	return p
}
