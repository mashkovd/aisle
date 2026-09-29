package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/adapter/claude"
	"github.com/mashkovd/aisle/internal/adapter/codex"
	"github.com/mashkovd/aisle/internal/runtime/proc"
	"github.com/mashkovd/aisle/internal/runtime/tmux"
	"github.com/mashkovd/aisle/internal/session"
	"github.com/mashkovd/aisle/internal/status"
	"github.com/mashkovd/aisle/internal/worktree"
)

func readers() []adapter.Adapter {
	o := adapter.Options{Home: "/nonexistent"}
	return []adapter.Adapter{claude.New(o), codex.New(o)}
}

func table(lines ...string) func(context.Context) (proc.Table, error) {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return func(context.Context) (proc.Table, error) { return proc.Parse(out), nil }
}

func TestInferLinksOnlyProvenRuntimes(t *testing.T) {
	s := &Service{Adapters: readers(), Procs: table(
		"101 100 /bin/zsh -l",
		"102 101 /usr/bin/script -q /dev/null /opt/homebrew/bin/claude --remote-control X --resume c-1 --dangerously-skip-permissions",
		"201 200 node /usr/local/bin/codex --model m resume x-1",
		"301 300 claude -c", // continues "the latest" conversation: no proof which
		"401 400 claude --resume c-2",
		"501 500 claude --resume c-3",
		"601 600 claude --session-id c-4",
	)}
	ss := []session.Session{
		{Engine: "claude", NativeID: "c-1"}, {Engine: "codex", NativeID: "x-1"},
		{Engine: "claude", NativeID: "c-2"}, {Engine: "claude", NativeID: "c-3"}, {Engine: "claude", NativeID: "c-4"},
	}
	ss[2].Runtime = &session.Runtime{Name: "already"} // linked by label: keep it
	rts := []session.Runtime{
		{Name: "foreign-claude", PID: 100},
		{Name: "foreign-codex", PID: 200},
		{Name: "continue", PID: 300},
		{Name: "second", PID: 400},
		{Name: "wrong-label", PID: 500, Engine: "codex"}, // aisle started codex here
		{Name: "aisle-new", PID: 600, Engine: "claude"},  // aisle started claude, id unknown then
	}
	ss, rest := s.infer(context.Background(), ss, rts)

	want := map[string]string{"c-1": "foreign-claude", "x-1": "foreign-codex", "c-2": "already", "c-4": "aisle-new"}
	for _, x := range ss {
		got := ""
		if x.Runtime != nil {
			got = x.Runtime.Name
		}
		if got != want[x.NativeID] {
			t.Errorf("%s: runtime %q, want %q", x.NativeID, got, want[x.NativeID])
		}
		if x.Runtime != nil && x.Runtime.Name != "already" {
			if inferred := x.Runtime.Name != "aisle-new"; x.Runtime.Inferred != inferred || x.Runtime.Managed() == inferred {
				t.Errorf("%s: inferred=%v managed=%v", x.Runtime.Name, x.Runtime.Inferred, x.Runtime.Managed())
			}
		}
	}
	var left []string
	for _, r := range rest {
		left = append(left, r.Name)
	}
	if fmt.Sprint(left) != "[continue second wrong-label]" {
		t.Errorf("unlinked %v", left)
	}
}

// TestLiveTmux runs fake agents in a private tmux server: a pane that asks,
// a pane that works, one whose agent exited, and one aisle did not start that
// is linked through its process arguments.
func TestLiveTmux(t *testing.T) {
	c := tmux.NewClient()
	if !c.Available() {
		t.Skip("tmux not installed")
	}
	c.Socket = fmt.Sprintf("aisle-live-%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command(c.Bin, "-L", c.Socket, "kill-server").Run() })

	bin := t.TempDir()
	fake := filepath.Join(bin, "claude")
	script := "#!/bin/sh\ncase \"$*\" in\n*ask*) printf 'Run it?\\n  Do you want to proceed?\\n' ;;\n*work*) while :; do date +%s%N; sleep 0.1; done ;;\nesac\nsleep 60\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(name string, argv ...string) {
		if err := c.Start(name, adapter.Command{Argv: argv, Dir: bin}, "claude", ""); err != nil {
			t.Fatal(err)
		}
	}
	start("asks", fake, "--resume", "id-ask")
	start("works", fake, "--resume", "id-work")
	start("quits", "true")
	// a session aisle did not start
	if err := exec.Command(c.Bin, "-L", c.Socket, "new-session", "-d", "-s", "foreign", fake, "--resume", "id-foreign").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)

	s := &Service{Adapters: readers(), Tmux: c}
	rts, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	ss := []session.Session{
		{Engine: "claude", NativeID: "id-ask"}, {Engine: "claude", NativeID: "id-work"}, {Engine: "claude", NativeID: "id-foreign"},
	}
	var snap Snapshot
	snap.Sessions, snap.Unlinked = session.Link(ss, rts)
	snap.Sessions, snap.Unlinked = s.infer(context.Background(), snap.Sessions, snap.Unlinked)
	for _, x := range snap.Sessions {
		if x.Runtime == nil {
			t.Fatalf("%s not linked; unlinked: %+v", x.NativeID, snap.Unlinked)
		}
	}
	if !snap.Sessions[2].Runtime.Inferred || snap.Sessions[2].Runtime.Name != "foreign" {
		t.Errorf("foreign: %+v", snap.Sessions[2].Runtime)
	}

	s.Observe(&snap)
	time.Sleep(400 * time.Millisecond)
	s.Observe(&snap)
	got := map[string]string{}
	for _, x := range snap.Sessions {
		got[x.Runtime.Name] = x.Runtime.Status
	}
	for _, rt := range snap.Unlinked {
		got[rt.Name] = rt.Status
	}
	want := map[string]status.State{"asks": status.Asking, "works": status.Working, "quits": status.Exited, "foreign": status.Idle}
	for name, w := range want {
		if got[name] != string(w) {
			t.Errorf("%s: %q, want %q", name, got[name], w)
		}
	}

	// Relink: a tmux session that ended loses its runtime, the others stay
	// linked (by label and by argv), and the snapshot passed in is untouched
	if err := exec.Command(c.Bin, "-L", c.Socket, "kill-session", "-t", "=asks").Run(); err != nil {
		t.Fatal(err)
	}
	next := s.Relink(context.Background(), snap)
	byID := map[string]*session.Runtime{}
	for _, x := range next.Sessions {
		byID[x.NativeID] = x.Runtime
	}
	if byID["id-ask"] != nil || byID["id-work"] == nil || byID["id-foreign"] == nil || !byID["id-foreign"].Inferred {
		t.Errorf("after relink: ask=%v work=%v foreign=%v", byID["id-ask"], byID["id-work"], byID["id-foreign"])
	}
	if snap.Sessions[0].Runtime == nil || snap.Sessions[0].Runtime.Name != "asks" {
		t.Error("Relink modified the snapshot it was given")
	}
}

func TestRemoveWorktreeRefusesWhileAgentWorks(t *testing.T) {
	c := tmux.NewClient()
	if !c.Available() {
		t.Skip("tmux not installed")
	}
	c.Socket = fmt.Sprintf("aisle-wt-%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command(c.Bin, "-L", c.Socket, "kill-server").Run() })
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "i"}} {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	wt, err := worktree.Create(dir, "busy", "claude")
	if err != nil {
		t.Fatal(err)
	}
	// the agent works in a subdirectory of the worktree
	sub := filepath.Join(wt.Root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("agent", adapter.Command{Argv: []string{"sleep", "30"}, Dir: sub}, "claude", ""); err != nil {
		t.Fatal(err)
	}
	s := &Service{Tmux: c}
	if _, err := s.RemoveWorktree(dir, "busy"); err == nil || !strings.Contains(err.Error(), "tmux session agent still works in busy") {
		t.Fatalf("got %v", err)
	}
	if err := exec.Command(c.Bin, "-L", c.Socket, "kill-session", "-t", "=agent").Run(); err != nil {
		t.Fatal(err)
	}
	if r, err := s.RemoveWorktree(dir, "busy"); err != nil || !r.BranchDeleted {
		t.Fatalf("after the session ended: %+v %v", r, err)
	}
}
