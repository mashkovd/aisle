package rules

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite want.txt of the golden cases")

// copyTree copies a fixture, keeping symlinks as symlinks.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, out)
		case d.IsDir():
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	dir  string
	env  Env
	glob bool
}

func load(t *testing.T, name string) fixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join("..", "..", "testdata", "rules", name), dir)
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	tf := filepath.Join(home, ".gemini", "trustedFolders.json")
	if b, err := os.ReadFile(tf); err == nil {
		b = bytes.ReplaceAll(b, []byte("PROJECT"), []byte(filepath.Join(dir, "project")))
		if err := os.WriteFile(tf, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := DefaultEnv(home, func(string) string { return "" })
	env.ClaudeVersion = "2.1.283 (Claude Code)"
	if b, err := os.ReadFile(filepath.Join(dir, "claude-version")); err == nil {
		env.ClaudeVersion = strings.TrimSpace(string(b))
	}
	return fixture{dir: dir, env: env, glob: exists(filepath.Join(dir, "global"))}
}

func (f fixture) check() Report {
	if f.glob {
		return Global(f.env, filepath.Join(f.env.CodexDir, "AGENTS.md"))
	}
	return Project(f.env, filepath.Join(f.dir, "project"))
}

// render is the golden form of a report: reaches, then findings.
func render(r Report, dir string) string {
	var b strings.Builder
	d := func(p string) string { return Display(p, dir, "") }
	for _, a := range r.Agents {
		fmt.Fprintf(&b, "agent %s %s", a.Agent, a.Reach)
		if a.Via != "" {
			fmt.Fprintf(&b, " via=%s", d(a.Via))
		}
		b.WriteString("\n")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "%s %s %s %s", f.Level, orAll(f.Agent), f.Code, d(f.File))
		if f.Line > 0 {
			fmt.Fprintf(&b, ":%d", f.Line)
		}
		if f.Fix != nil {
			fmt.Fprintf(&b, " fix=%s", f.Fix.Kind)
			switch f.Fix.Kind {
			case OpAddImport, OpCreate:
				fmt.Fprintf(&b, "(%s)", strings.TrimSpace(f.Fix.Text))
			case OpReplaceImport:
				fmt.Fprintf(&b, "(%s→%s)", f.Fix.Old, f.Fix.New)
			case OpGeminiNames:
				fmt.Fprintf(&b, "(%s)", strings.Join(f.Fix.Names, ","))
			}
		}
		if f.Unverified {
			b.WriteString(" unverified")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func orAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

func cases(t *testing.T) []string {
	ents, err := os.ReadDir(filepath.Join("..", "..", "testdata", "rules"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestGolden(t *testing.T) {
	for _, name := range cases(t) {
		t.Run(name, func(t *testing.T) {
			f := load(t, name)
			got := render(f.check(), f.dir)
			want := filepath.Join("..", "..", "testdata", "rules", name, "want.txt")
			if *update {
				if err := os.WriteFile(want, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			b, err := os.ReadFile(want)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/rules -update)", err)
			}
			if got != string(b) {
				t.Errorf("report differs\n--- got\n%s--- want\n%s", got, b)
			}
		})
	}
}

// TestSyncConverges applies every case's plan: afterwards nothing is left to
// fix, a second apply changes nothing, and every edited file has a backup
// holding its original content.
func TestSyncConverges(t *testing.T) {
	for _, name := range cases(t) {
		t.Run(name, func(t *testing.T) {
			f := load(t, name)
			ops, _ := Plan(f.check())
			before := map[string][]byte{}
			for _, o := range ops {
				if b, err := os.ReadFile(o.Path); err == nil {
					before[o.Path] = b
				}
			}
			res, err := Apply(ops)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range res {
				if orig, ok := before[r.Op.Path]; ok && r.Changed {
					b, err := os.ReadFile(r.Backup)
					if err != nil || !bytes.Equal(b, orig) {
						t.Errorf("%s: backup %q does not hold the original (%v)", r.Op.Path, r.Backup, err)
					}
				}
			}
			after := f.check()
			if left, _ := Plan(after); len(left) > 0 {
				t.Errorf("fixes left after apply: %+v\n%s", left, render(after, f.dir))
			}
			for _, fd := range after.Findings {
				if fd.Code == "broken-import" || strings.HasSuffix(fd.Code, "not-included") || fd.Code == "gemini-legacy-key" {
					t.Errorf("still reported after apply: %s %s", fd.Code, fd.Message)
				}
			}
			again, err := Apply(ops)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range again {
				if r.Changed {
					t.Errorf("second apply changed %s", r.Op.Path)
				}
			}
		})
	}
}

func TestMigratePreservesSettings(t *testing.T) {
	f := load(t, "gemini-legacy-key")
	p := filepath.Join(f.env.GeminiDir, "settings.json")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply([]Op{{Kind: OpGeminiMigrate, Path: p}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "{\n  \"ide\": {\n    \"hasSeenNudge\": true\n  },\n  \"context\": {\n    \"fileName\": [\n      \"GEMINI.md\",\n      \"AGENTS.md\"\n    ]\n  }\n}\n"
	if string(b) != want {
		t.Errorf("settings:\n%s\nwant:\n%s", b, want)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600 kept", st.Mode().Perm())
	}
}

func TestMigrateKeepsExistingContext(t *testing.T) {
	f := load(t, "global-legacy-key")
	p := filepath.Join(f.env.GeminiDir, "settings.json")
	if _, err := Apply([]Op{{Kind: OpGeminiMigrate, Path: p}}); err != nil {
		t.Fatal(err)
	}
	s := readGeminiSettings(p)
	if s.Legacy || strings.Join(s.Names, ",") != "GEMINI.md,AGENTS.md" {
		t.Errorf("legacy=%v names=%v; want the old key gone and context.fileName untouched", s.Legacy, s.Names)
	}
	if b, _ := os.ReadFile(p); !bytes.Contains(b, []byte("https://example.test/mcp")) {
		t.Error("other settings were lost")
	}
}

func TestSettingsWithCommentsAreNotRewritten(t *testing.T) {
	f := load(t, "gemini-settings-comments")
	p := filepath.Join(f.dir, "project", ".gemini", "settings.json")
	orig, _ := os.ReadFile(p)
	if _, err := Apply([]Op{{Kind: OpGeminiNames, Path: p, Names: []string{"AGENTS.md"}}}); err == nil {
		t.Error("want an error for a settings file that is not plain JSON")
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, orig) {
		t.Error("file was modified")
	}
}

func TestCreateNeverOverwrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(p, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Apply([]Op{{Kind: OpCreate, Path: p, Text: "@AGENTS.md\n"}})
	if err != nil || res[0].Changed {
		t.Fatalf("changed=%v err=%v", res[0].Changed, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine\n" {
		t.Errorf("overwritten: %q", b)
	}
}

func TestAddImportAppends(t *testing.T) {
	for in, want := range map[string]string{
		"":             "@AGENTS.md\n",
		"# A":          "# A\n\n@AGENTS.md\n",
		"# A\n":        "# A\n\n@AGENTS.md\n",
		"# A\n\n":      "# A\n\n@AGENTS.md\n",
		"@AGENTS.md\n": "@AGENTS.md\n",
	} {
		got, _ := Op{Kind: OpAddImport, Text: "@AGENTS.md"}.transform([]byte(in), true)
		if string(got) != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestReplaceImportIsWordExact(t *testing.T) {
	o := Op{Kind: OpReplaceImport, Line: 2, Old: "@AGENTS.md", New: "@../AGENTS.md"}
	got, err := o.transform([]byte("x\nsee @AGENTS.md.bak and @AGENTS.md.\n"), true)
	if err != nil || string(got) != "x\nsee @AGENTS.md.bak and @../AGENTS.md.\n" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := o.transform([]byte("x\nchanged\n"), true); err == nil {
		t.Error("want an error when the line no longer holds the include")
	}
}

func TestInit(t *testing.T) {
	root := t.TempDir()
	ops, err := Init(root, "")
	if err != nil || len(ops) != 1 || ops[0].Kind != OpCreate {
		t.Fatalf("stub: %+v %v", ops, err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, "../x.md"); err == nil {
		t.Error("want an error for --from outside the project")
	}
	if _, err := Init(root, "GEMINI.md"); err == nil {
		t.Error("want an error for a missing --from")
	}
	ops, err = Init(root, "CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ops); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md")); string(b) != "# mine\n" || exists(filepath.Join(root, "CLAUDE.md")) {
		t.Errorf("move failed: AGENTS.md=%q", b)
	}
	if _, err := Init(root, "CLAUDE.md"); err == nil {
		t.Error("want an error when AGENTS.md exists and --from is given")
	}
}

func TestPlanKeepsOnlySafeOpsOutsideProject(t *testing.T) {
	r := Report{Scope: "project", Root: "/p", Findings: []Finding{
		{Fix: &Op{Kind: OpGeminiNames, Path: "/p/.gemini/settings.json"}},
		{Fix: &Op{Kind: OpGeminiMigrate, Path: "/home/.gemini/settings.json"}},
		{Fix: &Op{Kind: OpReplaceImport, Path: "/home/.claude/CLAUDE.md"}},
		{Fix: &Op{Kind: OpGeminiMigrate, Path: "/home/.gemini/settings.json"}},
	}}
	ops, skipped := Plan(r)
	if len(ops) != 2 || len(skipped) != 1 || skipped[0].Kind != OpReplaceImport {
		t.Errorf("ops=%+v skipped=%+v", ops, skipped)
	}
}

func TestAgySizeLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), bytes.Repeat([]byte("x"), agyMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Project(DefaultEnv(t.TempDir(), func(string) string { return "" }), root)
	if !strings.Contains(render(r, root), "agy-size-limit") {
		t.Error("want agy-size-limit")
	}
}

func TestVersionAtLeast(t *testing.T) {
	for v, want := range map[string]bool{
		"2.1.277 (Claude Code)": true, "2.1.276": false, "2.2.0": true, "1.9.999": false, "": true, "unknown": true,
	} {
		if got := versionAtLeast(v, claudeNativeSince); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}

func TestImportArg(t *testing.T) {
	for _, c := range []struct{ file, target, root, want string }{
		{"/h/p/.claude/CLAUDE.md", "/h/p/AGENTS.md", "/h/p", "../AGENTS.md"},
		{"/h/p/CLAUDE.md", "/h/p/AGENTS.md", "/h/p", "AGENTS.md"},
		{"/h/.claude/CLAUDE.md", "/h/.codex/AGENTS.md", "", "~/.codex/AGENTS.md"},
	} {
		if got := importArg(c.file, c.target, "/h", c.root); got != c.want {
			t.Errorf("%s → %s: %s, want %s", c.file, c.target, got, c.want)
		}
	}
}
