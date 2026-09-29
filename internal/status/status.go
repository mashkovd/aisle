// Package status tells what an agent running in a terminal is doing, from
// what its terminal shows.
//
// tmux's own activity timestamp cannot be used: Antigravity redraws its
// screen every two seconds even when idle, while Claude Code streams a
// reply without showing a spinner. So a pane is judged by its screen: a
// question or menu at the bottom means the agent needs you; a spinner, or a
// screen that changed since the last look, means it is working; anything
// else means it finished and waits for your next prompt.
package status

import (
	"path/filepath"
	"regexp"
	"strings"
)

type State string

const (
	Unknown State = ""
	Working State = "working"
	Asking  State = "asking" // a question, permission prompt or menu is open
	Idle    State = "idle"   // done; waiting for the next prompt
	Exited  State = "exited" // the agent quit; its pane is back at the shell
)

// NeedsYou reports states that wait on the user.
func (s State) NeedsYou() bool { return s == Asking }

// bottom is how many non-empty lines at the end of the screen are searched:
// prompts and spinners sit there, above only the input box and status bar.
const bottom = 16

var (
	asking = []*regexp.Regexp{
		regexp.MustCompile(`^\s*[❯›>]\s*1\.\s`), // a numbered menu with the cursor on it
		regexp.MustCompile(`Do you want to `),
		regexp.MustCompile(`Do you trust `),
		regexp.MustCompile(`(?i)\benter to confirm\b|\benter Confirm\b|press enter to (confirm|continue)`),
		regexp.MustCompile(`Run this command\?`),
		regexp.MustCompile(`Would you like to `),
		regexp.MustCompile(`\([yY]/[nN]\)`),
	}
	working = []*regexp.Regexp{
		regexp.MustCompile(`(?i)esc to interrupt`),
		regexp.MustCompile(`\(\d+s\s*[·•]`),             // Claude Code / Codex elapsed-time spinner
		regexp.MustCompile(`(?i)\(esc to cancel, \d+s`), // Gemini CLI
		regexp.MustCompile(`Generating(\.\.\.|…)`),      // Antigravity
	}
	shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "nu": true}
)

// Classify judges one pane. prev is the screen seen on the previous look,
// empty when there was none; paneCommand is tmux's pane_current_command.
func Classify(screen, prev, paneCommand string) State {
	if shells[strings.TrimPrefix(filepath.Base(paneCommand), "-")] {
		return Exited
	}
	lines := tail(screen, bottom)
	for _, l := range lines {
		for _, re := range asking {
			if re.MatchString(l) {
				return Asking
			}
		}
	}
	for _, l := range lines {
		for _, re := range working {
			if re.MatchString(l) {
				return Working
			}
		}
	}
	if prev != "" && normalize(prev) != normalize(screen) {
		return Working
	}
	return Idle
}

func tail(screen string, n int) []string {
	var out []string
	ls := strings.Split(screen, "\n")
	for i := len(ls) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(ls[i]) != "" {
			out = append(out, ls[i])
		}
	}
	return out
}

// normalize drops trailing blanks so a redraw of the same content compares
// equal.
func normalize(s string) string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimRight(strings.Join(ls, "\n"), "\n")
}
