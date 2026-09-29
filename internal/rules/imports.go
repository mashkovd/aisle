package rules

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Import is one include directive found in an instruction file.
type Import struct {
	Line  int    // 1-based
	Token string // as written, e.g. "@AGENTS.md" or "@[rules](AGENTS.md)"
	Arg   string // the path part as written
	Path  string // resolved against the importing file's directory
}

func (i Import) exists() bool { return exists(i.Path) }

var (
	// Claude Code and Gemini CLI: `@path` at the start of a line or after
	// whitespace. Mentions without a dot or slash (`@claude`) are not paths.
	atImport = regexp.MustCompile(`(?:^|\s)(@[^\s@` + "`" + `]+)`)
	// Antigravity: `@[label](path)`.
	linkImport = regexp.MustCompile(`@\[[^\]]*\]\(([^)\s]+)\)`)
	inlineCode = regexp.MustCompile("`[^`]*`")
)

// Syntax selects which include form a parser recognises.
type Syntax int

const (
	AtPath Syntax = iota // Claude Code, Gemini CLI
	AtLink               // Antigravity
)

// parseImports lists the includes in file, skipping fenced and inline code
// the way the agents do. Paths resolve relative to the file, with ~ = home.
func parseImports(file, home string, syn Syntax) []Import {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	return scanImports(data, filepath.Dir(file), home, syn)
}

func scanImports(data []byte, dir, home string, syn Syntax) []Import {
	var out []Import
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	fenced := false
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		line = inlineCode.ReplaceAllStringFunc(line, func(s string) string { return strings.Repeat(" ", len(s)) })
		switch syn {
		case AtPath:
			for _, m := range atImport.FindAllStringSubmatch(line, -1) {
				tok := strings.TrimRight(m[1], ".,;:!?)]}\"'")
				arg := tok[1:]
				if strings.HasPrefix(arg, "[") || !strings.ContainsAny(arg, "./") {
					continue
				}
				out = append(out, Import{Line: n, Token: tok, Arg: arg, Path: resolve(arg, dir, home)})
			}
		case AtLink:
			for _, m := range linkImport.FindAllStringSubmatch(line, -1) {
				out = append(out, Import{Line: n, Token: m[0], Arg: m[1], Path: resolve(m[1], dir, home)})
			}
		}
	}
	return out
}

func resolve(arg, dir, home string) string {
	switch {
	case arg == "~":
		return home
	case strings.HasPrefix(arg, "~/"):
		return filepath.Join(home, arg[2:])
	case filepath.IsAbs(arg):
		return filepath.Clean(arg)
	}
	return filepath.Join(dir, arg)
}

// reaches follows includes from file, depth-first up to maxDepth hops, and
// returns the file whose include (or which itself, via a symlink) is target.
// Broken includes met on the way are collected.
func reaches(file, target, home string, syn Syntax, maxDepth int) (via string, broken []brokenImport) {
	seen := map[string]bool{}
	var walk func(f string, depth int)
	walk = func(f string, depth int) {
		real, err := filepath.EvalSymlinks(f)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		if via == "" && sameFile(f, target) {
			via = f
			return
		}
		if depth >= maxDepth {
			return
		}
		for _, imp := range parseImports(f, home, syn) {
			if !imp.exists() {
				broken = append(broken, brokenImport{File: f, Import: imp})
				continue
			}
			if via == "" && sameFile(imp.Path, target) {
				via = f
			}
			walk(imp.Path, depth+1)
		}
	}
	walk(file, 0)
	return via, broken
}

type brokenImport struct {
	File string
	Import
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// sameFile reports whether a and b are the same file after symlinks.
func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

func size(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// existing returns the paths under dir that exist, in order.
func existing(dir string, names ...string) []string {
	var out []string
	for _, n := range names {
		if p := filepath.Join(dir, n); exists(p) {
			out = append(out, p)
		}
	}
	return out
}

// importArg is how file should spell an include of target: relative to the
// file's directory inside a project (root), else ~/… under home.
func importArg(file, target, home, root string) string {
	rel, err := filepath.Rel(filepath.Dir(file), target)
	if err != nil {
		return target
	}
	inProject := root != "" && within(target, root) && within(file, root)
	if !inProject && home != "" && strings.HasPrefix(rel, "..") && within(target, home) {
		return "~/" + target[len(home)+1:]
	}
	return rel
}

// within reports whether p is root or below it.
func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
