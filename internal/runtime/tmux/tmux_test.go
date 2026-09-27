package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

func TestName(t *testing.T) {
	cases := []struct{ engine, project, id, want string }{
		{"claude", "/Users/me/work/api.v2", "deadbeefcafe0001", "claude-api-v2-deadbeef"},
		{"codex", "", "", "codex-general"},
		{"gemini", "/work/x:y/", "", "gemini-x-y"},
	}
	for _, c := range cases {
		if got := Name(c.engine, c.project, c.id); got != c.want {
			t.Errorf("Name(%q,%q,%q) = %q, want %q", c.engine, c.project, c.id, got, c.want)
		}
	}
}

// The vertical slice: a runtime started for a conversation is labelled, and
// the next discovery links it back to that exact conversation.
func TestStartLabelsAndLinks(t *testing.T) {
	c := NewClient()
	if !c.Available() {
		t.Skip("tmux not installed")
	}
	c.Socket = fmt.Sprintf("aisle-test-%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command(c.Bin, "-L", c.Socket, "kill-server").Run() })

	dir := t.TempDir()
	cmd := adapter.Command{Argv: []string{"sleep", "30"}, Dir: dir}
	name := Name("claude", dir, "abcdef0123")
	if err := c.Start(name, cmd, "claude", "abcdef0123"); err != nil {
		t.Fatal(err)
	}
	if err := c.Start("foreign", adapter.Command{Argv: []string{"sleep", "30"}}, "", ""); err != nil {
		t.Fatal(err)
	}
	if !c.Has(name) || c.Has(name[:len(name)-1]) {
		t.Fatal("Has must match exact names only")
	}
	if got := c.FreeName(name); got != name+"-2" {
		t.Errorf("FreeName = %q", got)
	}

	rts, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	ss := []session.Session{{Engine: "claude", NativeID: "abcdef0123", Project: dir}}
	linked, rest := session.Link(ss, rts)
	if linked[0].Runtime == nil || linked[0].Runtime.Name != name {
		t.Fatalf("not linked: %+v (runtimes %+v)", linked[0], rts)
	}
	if len(rest) != 1 || rest[0].Name != "foreign" || rest[0].Managed() {
		t.Fatalf("foreign session should stay unmanaged: %+v", rest)
	}
}
