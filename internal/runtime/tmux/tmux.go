// Package tmux is the only runtime aisle supports: it lists sessions, launches
// labelled ones and hands the terminal over to them.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"al.essio.dev/pkg/shellescape"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

const (
	optEngine = "@aisle_engine"
	optID     = "@aisle_id"
)

var ErrNotInstalled = errors.New("tmux is not installed (brew install tmux)")

type Client struct {
	Bin    string // resolved tmux binary; empty when not installed
	Socket string // optional -L socket name, used by tests to isolate the server
}

func NewClient() *Client {
	bin, _ := exec.LookPath("tmux")
	return &Client{Bin: bin}
}

func (c *Client) Available() bool { return c.Bin != "" }

func (c *Client) run(args ...string) ([]byte, error) {
	if !c.Available() {
		return nil, ErrNotInstalled
	}
	if c.Socket != "" {
		args = append([]string{"-L", c.Socket}, args...)
	}
	var stderr bytes.Buffer
	cmd := exec.Command(c.Bin, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// List returns every tmux session; no server running means no sessions.
func (c *Client) List() ([]session.Runtime, error) {
	if !c.Available() {
		return nil, nil
	}
	format := strings.Join([]string{
		"#{session_name}", "#{session_attached}", "#{pane_current_path}",
		"#{pane_current_command}", "#{" + optEngine + "}", "#{" + optID + "}",
	}, "\t")
	out, err := c.run("list-sessions", "-F", format)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting") {
			return nil, nil
		}
		return nil, err
	}
	var rts []session.Runtime
	// trim only newlines: trailing tabs are empty label fields
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		p := strings.Split(l, "\t")
		if len(p) < 6 {
			continue
		}
		rts = append(rts, session.Runtime{
			Kind: "tmux", Name: p[0], Attached: p[1] != "0", Path: p[2], Command: p[3],
			Engine: p[4], NativeID: p[5],
		})
	}
	return rts, nil
}

// Current returns the tmux session this process runs in, if any.
func (c *Client) Current() string {
	if os.Getenv("TMUX") == "" || !c.Available() {
		return ""
	}
	out, err := c.run("display-message", "-p", "#{session_name}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (c *Client) Has(name string) bool {
	_, err := c.run("has-session", "-t", "="+name)
	return err == nil
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// Name builds the tmux session name: <engine>-<project>[-<id8>].
func Name(engine, project, nativeID string) string {
	base := filepath.Base(strings.TrimRight(project, "/"))
	if project == "" || base == "." || base == "/" {
		base = "general"
	}
	n := engine + "-" + strings.Trim(unsafe.ReplaceAllString(base, "-"), "-")
	if nativeID != "" {
		id := nativeID
		if len(id) > 8 {
			id = id[:8]
		}
		n += "-" + id
	}
	return n
}

// FreeName returns name, or name-2, name-3… whichever does not exist yet.
func (c *Client) FreeName(name string) string {
	if !c.Has(name) {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !c.Has(n) {
			return n
		}
	}
}

// Launch starts a labelled session and takes over the terminal. It only
// returns on error.
func (c *Client) Launch(name string, cmd adapter.Command, engine, nativeID string) error {
	if err := c.Start(name, cmd, engine, nativeID); err != nil {
		return err
	}
	return c.Attach(name)
}

// Start creates a detached session running cmd and labels it with the
// conversation it hosts, so later discovery can link the two.
func (c *Client) Start(name string, cmd adapter.Command, engine, nativeID string) error {
	dir := cmd.Dir
	if st, err := os.Stat(dir); dir == "" || err != nil || !st.IsDir() {
		dir, _ = os.Getwd()
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	// keep the pane open after the agent exits
	script := shellescape.QuoteCommand(cmd.Argv) + "; exec " + shellescape.Quote(shell) + " -l"
	if _, err := c.run("new-session", "-d", "-s", name, "-c", dir, script); err != nil {
		return err
	}
	// set-option takes a pane target: "=name:" is the exact session name
	if _, err := c.run("set-option", "-t", "="+name+":", optEngine, engine); err != nil {
		return err
	}
	if nativeID != "" {
		if _, err := c.run("set-option", "-t", "="+name+":", optID, nativeID); err != nil {
			return err
		}
	}
	return nil
}

// Attach switches the current client (inside tmux) or attaches the terminal
// (outside tmux) to name, replacing this process.
func (c *Client) Attach(name string) error {
	if !c.Available() {
		return ErrNotInstalled
	}
	if os.Getenv("TMUX") != "" {
		_, err := c.run("switch-client", "-t", "="+name)
		return err
	}
	argv := []string{"tmux"}
	if c.Socket != "" {
		argv = append(argv, "-L", c.Socket)
	}
	return syscall.Exec(c.Bin, append(argv, "attach-session", "-t", "="+name), os.Environ())
}
