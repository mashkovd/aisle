// Package worktree gives a new agent session its own git worktree, so
// parallel sessions in one repository do not edit the same files.
//
// A worktree lives in <repo>/.worktrees/<name> on a new branch aisle/<name>
// cut from HEAD. The directory is excluded through .git/info/exclude, so
// the repository itself does not change. aisle never removes a worktree;
// `aisle worktree rm` (or `git worktree remove`) does when you are done.
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
	"strconv"
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
	repo, err := mainRoot(dir) // the main worktree, even when dir is in a linked one
	if err != nil {
		return Worktree{}, err
	}
	common := filepath.Join(repo, ".git")

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

// Info describes one worktree under <repo>/.worktrees.
type Info struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"` // empty when detached
	Changes int    `json:"changes"`          // uncommitted changes, untracked files included
	Ahead   int    `json:"ahead"`            // commits on its branch that the main worktree's HEAD lacks
}

// List returns the repository's root and the worktrees aisle manages in
// it, i.e. those under <repo>/.worktrees.
func List(dir string) (string, []Info, error) {
	repo, err := mainRoot(dir)
	if err != nil {
		return "", nil, err
	}
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", nil, err
	}
	base := filepath.Join(repo, dirName) + string(filepath.Separator)
	var infos []Info
	for _, block := range strings.Split(out, "\n\n") {
		var wt Info
		for _, l := range strings.Split(block, "\n") {
			if p, ok := strings.CutPrefix(l, "worktree "); ok {
				wt.Path = p
			}
			if b, ok := strings.CutPrefix(l, "branch refs/heads/"); ok {
				wt.Branch = b
			}
		}
		if !strings.HasPrefix(wt.Path, base) || strings.Contains(strings.TrimPrefix(wt.Path, base), string(filepath.Separator)) {
			continue
		}
		wt.Name = filepath.Base(wt.Path)
		if st, err := git(wt.Path, "status", "--porcelain"); err == nil && st != "" {
			wt.Changes = len(strings.Split(st, "\n"))
		}
		if wt.Branch != "" {
			if n, err := git(repo, "rev-list", "--count", "HEAD.."+wt.Branch); err == nil {
				wt.Ahead, _ = strconv.Atoi(n)
			}
		}
		infos = append(infos, wt)
	}
	return repo, infos, nil
}

// Removed reports what Remove did with the branch.
type Removed struct {
	Info
	BranchDeleted bool   `json:"branch_deleted"`
	BranchKept    string `json:"branch_kept,omitempty"` // why the branch was kept
}

// ErrNotFound means there is no aisle worktree by that name.
var ErrNotFound = errors.New("no such worktree")

// Remove deletes the worktree name of dir's repository. It refuses when the
// worktree has uncommitted changes. Its branch is deleted only when it is
// an aisle/ branch fully merged into the main worktree's HEAD.
func Remove(dir, name string) (Removed, error) {
	repo, infos, err := List(dir)
	if err != nil {
		return Removed{}, err
	}
	var wt *Info
	for i := range infos {
		if infos[i].Name == name {
			wt = &infos[i]
		}
	}
	if wt == nil {
		return Removed{}, fmt.Errorf("%w %q in %s", ErrNotFound, name, filepath.Join(repo, dirName))
	}
	if wt.Changes > 0 {
		return Removed{}, fmt.Errorf("%s has %d uncommitted change(s); commit or stash them first", name, wt.Changes)
	}
	if _, err := git(repo, "worktree", "remove", wt.Path); err != nil {
		return Removed{}, err
	}
	r := Removed{Info: *wt}
	switch {
	case wt.Branch == "":
	case !strings.HasPrefix(wt.Branch, "aisle/"):
		r.BranchKept = "not created by aisle"
	case wt.Ahead > 0:
		r.BranchKept = fmt.Sprintf("%d commit(s) not merged", wt.Ahead)
	default:
		if _, err := git(repo, "branch", "-d", wt.Branch); err != nil {
			r.BranchKept = err.Error()
		} else {
			r.BranchDeleted = true
		}
	}
	return r, nil
}

func mainRoot(dir string) (string, error) {
	common, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not in a git repository", dir)
	}
	if filepath.Base(common) != ".git" {
		return "", errors.New("bare repositories are not supported")
	}
	return filepath.Dir(common), nil
}
