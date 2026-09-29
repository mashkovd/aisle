package rules

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
)

// Antigravity (agy):
//   - reads GEMINI.md and AGENTS.md and .agents/rules/*.md in a project,
//     ~/.gemini/GEMINI.md globally (both confirmed);
//   - follows only `@[label](path)` includes, not `@path` (confirmed);
//   - skips a file it has already read under another name only when both
//     names are the same file on disk;
//   - truncates each file at 24 000 bytes.
const agyMaxBytes = 24000

func agyProject(e Env, r *Report) {
	ar := AgentReport{Agent: "agy", Reach: Missing, Files: existing(r.Root, "GEMINI.md", "AGENTS.md")}
	rules, _ := filepath.Glob(filepath.Join(r.Root, ".agents", "rules", "*.md"))
	sort.Strings(rules)
	ar.Files = append(ar.Files, rules...)
	defer func() { r.Agents = append(r.Agents, ar) }()
	if r.Exists {
		ar.Reach = Native
		if g := filepath.Join(r.Root, "GEMINI.md"); exists(g) && !sameFile(g, r.Source) {
			agyDuplicate(e, r, g)
		}
	}
	agySizes(r, ar.Files)
}

func agyGlobal(e Env, r *Report) {
	p := filepath.Join(e.GeminiDir, "GEMINI.md")
	ar := AgentReport{Agent: "agy", Reach: Missing, Files: existing(e.GeminiDir, "GEMINI.md")}
	defer func() { r.Agents = append(r.Agents, ar) }()
	if len(ar.Files) > 0 {
		if via, _ := reaches(p, r.Source, e.Home, AtLink, 5); via != "" {
			ar.Reach, ar.Via = Imported, via
		}
	}
	if ar.Reach == Missing && r.Exists {
		msg := "Antigravity's global rules (~/.gemini/GEMINI.md) do not include " + Display(r.Source, "", e.Home)
		if exists(p) {
			msg += "; add @[AGENTS.md](" + importArg(p, r.Source, e.Home, r.Root) + ") to it"
		} else {
			msg += globalGeminiHint(e, r)
		}
		r.add(Finding{Agent: "agy", Level: Warn, Code: "agy-agents-md-not-loaded", File: p, Message: msg})
	}
	agySizes(r, ar.Files)
}

// agyDuplicate flags a GEMINI.md that repeats AGENTS.md: a copy of it, or an
// @[…](AGENTS.md) include of it.
func agyDuplicate(e Env, r *Report, g string) {
	why := ""
	if a, b := readFile(g), readFile(r.Source); len(a) > 0 && bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) {
		why = "is a copy of AGENTS.md"
	} else if via, _ := reaches(g, r.Source, e.Home, AtLink, 5); via != "" {
		why = "includes AGENTS.md"
	}
	if why != "" {
		r.add(Finding{Agent: "agy", Level: Warn, Code: "agy-duplicate", File: g,
			Message: "Antigravity reads both GEMINI.md and AGENTS.md, and GEMINI.md " + why + ", so it gets the rules twice; make GEMINI.md a symlink to AGENTS.md or remove it"})
	}
}

func agySizes(r *Report, files []string) {
	for _, f := range files {
		if n := size(f); n > agyMaxBytes {
			r.add(Finding{Agent: "agy", Level: Warn, Code: "agy-size-limit", File: f, Unverified: true,
				Message: fmt.Sprintf("%d bytes; Antigravity reads only the first %d of each file", n, agyMaxBytes)})
		}
	}
}
