package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Project checks the project at root: does its AGENTS.md reach Claude Code,
// Codex, Gemini CLI and Antigravity, and does any agent load it twice.
func Project(e Env, root string) Report {
	r := Report{Scope: "project", Root: root, Source: filepath.Join(root, "AGENTS.md")}
	r.Exists = exists(r.Source)
	if !r.Exists {
		r.add(Finding{Level: Error, Code: "no-agents-md", File: r.Source,
			Message: "no AGENTS.md here; `aisle rules init` creates one"})
	}
	claudeProject(e, &r)
	codexProject(e, &r)
	geminiProject(e, &r)
	agyProject(e, &r)
	importsNotExpanded(e, &r)
	return r
}

// Global checks the user-level instruction files against source, the
// global AGENTS.md. Codex is the one agent without includes, so the source
// lives where Codex reads it and the others include it.
func Global(e Env, source string) Report {
	r := Report{Scope: "global", Root: "", Source: source}
	r.Exists = exists(source)
	if !r.Exists {
		r.add(Finding{Level: Error, Code: "no-agents-md", File: source,
			Message: "the global AGENTS.md does not exist; Codex reads it from here, the other agents include it"})
	}
	claudeGlobal(e, &r)
	codexGlobal(e, &r)
	geminiGlobal(e, &r)
	agyGlobal(e, &r)
	importsNotExpanded(e, &r)
	return r
}

// importsNotExpanded flags `@path` includes in the source: Claude Code and
// Gemini CLI follow them, Codex and Antigravity read them as plain text.
func importsNotExpanded(e Env, r *Report) {
	if !r.Exists {
		return
	}
	var lines []int
	var first string
	for _, imp := range parseImports(r.Source, e.Home, AtPath) {
		if imp.exists() {
			if first == "" {
				first = imp.Token
			}
			lines = append(lines, imp.Line)
		}
	}
	if len(lines) == 0 {
		return
	}
	for _, agent := range []string{"codex", "agy"} {
		r.add(Finding{Agent: agent, Level: Warn, Code: "imports-not-expanded", File: r.Source, Line: lines[0],
			Message: agentName(agent) + " does not follow `@path` includes: " + first + " and " +
				strconv.Itoa(len(lines)-1) + " more reach it as plain text, not as the file's content"})
	}
}

func agentName(a string) string {
	switch a {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini CLI"
	case "agy":
		return "Antigravity"
	}
	return a
}

var semver = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// versionAtLeast compares the first x.y.z in v with want; an unknown
// version counts as new enough.
func versionAtLeast(v string, want [3]int) bool {
	m := semver.FindStringSubmatch(v)
	if m == nil {
		return true
	}
	for i := 0; i < 3; i++ {
		n, _ := strconv.Atoi(m[i+1])
		if n != want[i] {
			return n > want[i]
		}
	}
	return true
}

func readFile(p string) []byte { b, _ := os.ReadFile(p); return b }
