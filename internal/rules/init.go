package rules

import (
	"errors"
	"fmt"
	"path/filepath"
)

const stub = "# AGENTS.md\n\nInstructions for AI coding agents working in this repository: how to build and test,\nthe conventions to follow, and what an agent must not do.\n"

// Init returns the op that gives root an AGENTS.md. With from (a file in
// root, e.g. CLAUDE.md or GEMINI.md) that file is renamed to AGENTS.md, so
// its content becomes the shared rules; otherwise a short stub is created.
func Init(root, from string) ([]Op, error) {
	src := filepath.Join(root, "AGENTS.md")
	if exists(src) {
		if from != "" {
			return nil, errors.New("AGENTS.md already exists; --from would overwrite it")
		}
		return nil, nil
	}
	if from == "" {
		return []Op{{Kind: OpCreate, Path: src, Text: stub}}, nil
	}
	p := from
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if !within(p, root) {
		return nil, fmt.Errorf("--from %s is outside %s", from, root)
	}
	if !exists(p) {
		return nil, fmt.Errorf("--from %s: no such file", from)
	}
	return []Op{{Kind: OpMove, Path: p, To: src}}, nil
}

// Plan filters the fixes a report proposes down to what sync may apply:
// every fix inside the project, and only safe ones (an added include, a
// moved settings key) for files outside it.
func Plan(r Report) (ops []Op, skipped []Op) {
	for _, o := range r.Fixes() {
		inside := false
		if r.Scope == "project" && r.Root != "" {
			inside = within(o.Path, r.Root)
		}
		if inside || o.Safe() {
			ops = append(ops, o)
		} else {
			skipped = append(skipped, o)
		}
	}
	return ops, skipped
}
