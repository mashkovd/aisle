// Package worktree gives a new agent session its own git worktree, so
// parallel sessions in one repository do not edit the same files.
//
// A worktree lives in <repo>/.worktrees/<name> on a new branch aisle/<name>
// cut from HEAD. The directory is excluded through .git/info/exclude, so
// the repository itself does not change. aisle never removes a worktree;
// `git worktree remove` does when you are done.
package worktree

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const dirName = ".worktrees"

// Worktree is a created worktree.
type Worktree struct {
	Repo   string // the main worktree's root
	Name   string
	Path   string // where the agent starts: the worktree, or the same subdirectory in it
	Root   string // the worktree's root
	Branch string
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Create adds a worktree for the repository containing dir. An empty name
// picks <prefix>-<4 hex digits>.
func Create(dir, name, prefix string) (Worktree, error) {
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Worktree{}, fmt.Errorf("%s is not in a git repository", dir)
	}
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Worktree{}, err
	}
	if filepath.Base(common) != ".git" {
		return Worktree{}, errors.New("bare repositories are not supported")
	}
	repo := filepath.Dir(common) // the main worktree, even when dir is in a linked one

	if name == "" {
		for {
			name = prefix + "-" + randomHex(2)
			if !exists(filepath.Join(repo, dirName, name)) {
				break
			}
		}
	}
	if !validName.MatchString(name) || strings.Contains(name, "..") {
		return Worktree{}, fmt.Errorf("invalid worktree name %q: use letters, digits, '.', '_' and '-'", name)
	}
	wt := Worktree{Repo: repo, Name: name, Root: filepath.Join(repo, dirName, name), Branch: "aisle/" + name}
	if exists(wt.Root) {
		return Worktree{}, fmt.Errorf("%s already exists", wt.Root)
	}
	if err := exclude(repo, common); err != nil {
		return Worktree{}, err
	}
	if _, err := git(repo, "worktree", "add", "-b", wt.Branch, wt.Root, "HEAD"); err != nil {
		return Worktree{}, err
	}
	wt.Path = wt.Root
	// start in the same subdirectory the user was in, when it exists there
	if rel, err := filepath.Rel(top, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		if p := filepath.Join(wt.Root, rel); exists(p) {
			wt.Path = p
		}
	}
	return wt, nil
}

// exclude adds /.worktrees/ to .git/info/exclude unless git already
// ignores it.
func exclude(repo, common string) error {
	if _, err := git(repo, "check-ignore", "-q", dirName+"/x"); err == nil {
		return nil
	}
	p := filepath.Join(common, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		if b, err := os.ReadFile(p); err == nil && !strings.HasSuffix(string(b), "\n") {
			prefix = "\n"
		}
	}
	_, err = fmt.Fprintf(f, "%s# aisle: agent worktrees\n/%s/\n", prefix, dirName)
	return err
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
