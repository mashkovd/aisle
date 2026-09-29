package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := func(args ...string) { run(t, dir, append([]string{"git", "-C", dir}, args...)...) }
	g("init", "-q", "-b", "main")
	g("config", "user.email", "t@example.com")
	g("config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g("add", ".")
	g("commit", "-q", "-m", "init")
	return dir
}

func TestCreate(t *testing.T) {
	dir := repo(t)
	wt, err := Create(filepath.Join(dir, "sub"), "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wt.Name, "claude-") || wt.Root != filepath.Join(dir, ".worktrees", wt.Name) || wt.Branch != "aisle/"+wt.Name {
		t.Errorf("got %+v", wt)
	}
	if wt.Path != filepath.Join(wt.Root, "sub") {
		t.Errorf("path %s, want the same subdirectory", wt.Path)
	}
	if b := run(t, dir, "git", "-C", wt.Root, "branch", "--show-current"); b != wt.Branch {
		t.Errorf("branch %q", b)
	}
	if s := run(t, dir, "git", "-C", dir, "status", "--porcelain"); s != "" {
		t.Errorf("repository changed:\n%s", s)
	}

	// a second one, started from inside the first: lands in the main repo,
	// and the exclude line is not repeated
	wt2, err := Create(wt.Path, "fix-login", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if wt2.Root != filepath.Join(dir, ".worktrees", "fix-login") || wt2.Repo != dir {
		t.Errorf("got %+v", wt2)
	}
	ex, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(ex), "/.worktrees/") != 1 {
		t.Errorf("exclude:\n%s", ex)
	}
	if _, err := Create(dir, "fix-login", "codex"); err == nil {
		t.Error("want an error for an existing name")
	}
}

func TestRejects(t *testing.T) {
	dir := repo(t)
	for _, n := range []string{"../x", "a/b", "-x", "a..b", " "} {
		if _, err := Create(dir, n, "claude"); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
	if _, err := Create(t.TempDir(), "", "claude"); err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("non-repo: %v", err)
	}
}

func TestRespectsExistingIgnore(t *testing.T) {
	dir := repo(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "a", "claude"); err != nil {
		t.Fatal(err)
	}
	ex, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Contains(string(ex), "/.worktrees/") {
		t.Error("exclude written although .gitignore already covers it")
	}
}
