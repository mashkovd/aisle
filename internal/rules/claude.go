package rules

import (
	"fmt"
	"path/filepath"
)

// Claude Code:
//   - reads CLAUDE.md, .claude/CLAUDE.md and CLAUDE.local.md;
//   - from 2.1.277 reads AGENTS.md by itself, but only where none of those
//     exist (confirmed: a .claude/CLAUDE.md turns native reading off);
//   - follows `@path` includes up to 4 hops, relative to the including file
//     (confirmed: `@AGENTS.md` in .claude/CLAUDE.md means .claude/AGENTS.md).
var claudeNativeSince = [3]int{2, 1, 277}

const claudeHops = 4

var claudeFiles = []string{"CLAUDE.md", filepath.Join(".claude", "CLAUDE.md"), "CLAUDE.local.md"}

func claudeProject(e Env, r *Report) {
	ar := AgentReport{Agent: "claude", Reach: Missing}
	files := existing(r.Root, claudeFiles...)
	ar.Files = files
	defer func() { r.Agents = append(r.Agents, ar) }()

	if len(files) == 0 {
		if !r.Exists {
			return
		}
		if versionAtLeast(e.ClaudeVersion, claudeNativeSince) {
			ar.Reach, ar.Files = Native, []string{r.Source}
			return
		}
		p := filepath.Join(r.Root, "CLAUDE.md")
		r.add(Finding{Agent: "claude", Level: Error, Code: "claude-too-old", File: p,
			Message: fmt.Sprintf("Claude Code %s reads AGENTS.md by itself only from 2.1.277; update it or include AGENTS.md from a CLAUDE.md", e.ClaudeVersion),
			Fix:     &Op{Kind: OpCreate, Path: p, Text: "@AGENTS.md\n"}})
		return
	}
	claudeReach(e, r, &ar, files)
}

func claudeGlobal(e Env, r *Report) {
	ar := AgentReport{Agent: "claude", Reach: Missing}
	defer func() { r.Agents = append(r.Agents, ar) }()
	p := filepath.Join(e.ClaudeDir, "CLAUDE.md")
	if !exists(p) {
		if r.Exists {
			r.add(Finding{Agent: "claude", Level: Error, Code: "claude-agents-md-not-included", File: p,
				Message: "no global CLAUDE.md, so Claude Code never reads " + Display(r.Source, "", e.Home),
				Fix:     &Op{Kind: OpCreate, Path: p, Text: "@" + importArg(p, r.Source, e.Home, r.Root) + "\n"}})
		}
		return
	}
	ar.Files = []string{p}
	claudeReach(e, r, &ar, ar.Files)
}

// claudeReach follows the includes of the CLAUDE files and reports whether
// one of them reaches the source, fixing a broken include that was meant to.
func claudeReach(e Env, r *Report, ar *AgentReport, files []string) {
	var repair *Op
	for _, f := range files {
		via, broken := reaches(f, r.Source, e.Home, AtPath, claudeHops)
		for _, b := range broken {
			fd := Finding{Agent: "claude", Level: Warn, Code: "broken-import", File: b.File, Line: b.Line,
				Message: fmt.Sprintf("%s resolves to %s, which does not exist (includes are relative to the including file)",
					b.Token, Display(b.Path, r.Root, e.Home))}
			// the common slip: written relative to the project root
			if r.Root != "" {
				if meant := filepath.Join(r.Root, b.Arg); exists(meant) {
					fd.Fix = &Op{Kind: OpReplaceImport, Path: b.File, Line: b.Line, Old: b.Token,
						New: "@" + importArg(b.File, meant, e.Home, r.Root)}
					if sameFile(meant, r.Source) && repair == nil {
						repair = fd.Fix
					}
				}
			}
			r.add(fd)
		}
		if via != "" && ar.Reach == Missing {
			ar.Reach, ar.Via = Imported, via
		}
	}
	if ar.Reach != Missing || !r.Exists {
		return
	}
	fd := Finding{Agent: "claude", Level: Error, Code: "claude-agents-md-not-included", File: files[0],
		Message: fmt.Sprintf("Claude Code reads %s and nothing there includes %s",
			Display(files[0], r.Root, e.Home), Display(r.Source, r.Root, e.Home))}
	if r.Scope == "project" {
		fd.Message += " (with a CLAUDE.md present it does not read AGENTS.md by itself)"
	}
	if repair == nil {
		fd.Fix = &Op{Kind: OpAddImport, Path: files[0], Text: "@" + importArg(files[0], r.Source, e.Home, r.Root)}
	} else {
		fd.Message += "; fixing the broken include above is enough"
	}
	r.add(fd)
}
