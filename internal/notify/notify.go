// Package notify turns status changes of running agents into desktop
// notifications.
package notify

import (
	"errors"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
	"github.com/mashkovd/aisle/internal/status"
)

type Kind string

const (
	Asking Kind = "asking" // a session started waiting for you
	Done   Kind = "done"   // a session finished working
)

// Event is one notification-worthy change.
type Event struct {
	Kind    Kind
	Runtime string // tmux session name
	Engine  string
	Project string
	Summary string
}

func (e Event) Title() string {
	if e.Kind == Asking {
		return e.Engine + " needs you"
	}
	return e.Engine + " finished"
}

func (e Event) Body() string {
	var parts []string
	if p := strings.TrimRight(e.Project, "/"); p != "" {
		parts = append(parts, p[strings.LastIndex(p, "/")+1:])
	}
	if e.Summary != "" {
		parts = append(parts, e.Summary)
	} else {
		parts = append(parts, e.Runtime)
	}
	return strings.Join(parts, " · ")
}

// Watcher remembers the last status of every runtime and reports what
// changed. The first look only records: states found at start are not news.
type Watcher struct {
	On   map[Kind]bool
	prev map[string]status.State
}

// Changes returns the events between the previous snapshot and this one.
// A runtime that appears already waiting counts as starting to wait; done
// means it was seen working and is now idle.
func (w *Watcher) Changes(snap service.Snapshot) []Event {
	cur := map[string]status.State{}
	var events []Event
	see := func(rt session.Runtime, s *session.Session) {
		st := status.State(rt.Status)
		cur[rt.Name] = st
		if w.prev == nil {
			return
		}
		before, known := w.prev[rt.Name]
		ev := Event{Runtime: rt.Name, Engine: rt.Engine}
		if s != nil {
			ev.Engine, ev.Project, ev.Summary = s.Engine, s.Project, s.Summary
		} else {
			ev.Project = rt.Path
		}
		switch {
		case st == status.Asking && (!known || before != status.Asking):
			ev.Kind = Asking
		case st == status.Idle && known && before == status.Working:
			ev.Kind = Done
		default:
			return
		}
		if w.On[ev.Kind] {
			events = append(events, ev)
		}
	}
	for i := range snap.Sessions {
		if rt := snap.Sessions[i].Runtime; rt != nil {
			see(*rt, &snap.Sessions[i])
		}
	}
	for _, rt := range snap.Unlinked {
		if rt.Engine != "" {
			see(rt, nil)
		}
	}
	w.prev = cur
	sort.SliceStable(events, func(i, j int) bool { return events[i].Runtime < events[j].Runtime })
	return events
}

// Notifier shows a desktop notification.
type Notifier interface {
	Notify(e Event) error
}

// Command runs an external notifier. Title and text are passed as
// arguments, never spliced into a script: summaries come from
// conversations and may contain anything.
type Command struct {
	Argv func(e Event) []string
	run  func(name string, args ...string) error
}

func (c Command) Notify(e Event) error {
	argv := c.Argv(e)
	run := c.run
	if run == nil {
		run = func(name string, args ...string) error { return exec.Command(name, args...).Run() }
	}
	return run(argv[0], argv[1:]...)
}

// osascriptArgv shows a notification through Notification Center; a
// waiting agent also plays a sound.
func osascriptArgv(e Event) []string {
	sound := ""
	if e.Kind == Asking {
		sound = ` sound name "Glass"`
	}
	return []string{"osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)" + sound,
		"-e", "end run",
		safeArg(e.Title()), safeArg(e.Body())}
}

func notifySendArgv(e Event) []string {
	urgency := "normal"
	if e.Kind == Asking {
		urgency = "critical"
	}
	return []string{"notify-send", "--app-name=aisle", "--urgency=" + urgency, "--", e.Title(), e.Body()}
}

// safeArg keeps an argument from being read as an osascript option.
func safeArg(s string) string {
	if strings.HasPrefix(s, "-") {
		return " " + s
	}
	return s
}

// ErrUnsupported means no desktop notifier was found.
var ErrUnsupported = errors.New("no desktop notifier: needs macOS or notify-send")

// System returns the desktop notifier of this machine.
func System() (Notifier, error) {
	switch {
	case runtime.GOOS == "darwin":
		return Command{Argv: osascriptArgv}, nil
	case hasBinary("notify-send"):
		return Command{Argv: notifySendArgv}, nil
	}
	return nil, ErrUnsupported
}

func hasBinary(name string) bool { _, err := exec.LookPath(name); return err == nil }
