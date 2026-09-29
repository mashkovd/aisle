package notify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
)

func snap(st map[string]string, unlinked ...session.Runtime) service.Snapshot {
	var s service.Snapshot
	for _, name := range []string{"a", "b", "c"} {
		if v, ok := st[name]; ok {
			s.Sessions = append(s.Sessions, session.Session{Engine: "claude", NativeID: name, Project: "/w/api", Summary: "Fix " + name,
				Runtime: &session.Runtime{Name: "t" + name, Engine: "claude", Status: v}})
		}
	}
	s.Unlinked = unlinked
	return s
}

func kinds(evs []Event) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Runtime+":"+string(e.Kind))
	}
	return strings.Join(out, " ")
}

func TestChanges(t *testing.T) {
	w := &Watcher{On: map[Kind]bool{Asking: true, Done: true}}
	steps := []struct {
		st   map[string]string
		want string
	}{
		{map[string]string{"a": "asking", "b": "working"}, ""},         // found at start: not news
		{map[string]string{"a": "asking", "b": "working"}, ""},         // unchanged
		{map[string]string{"a": "idle", "b": "idle"}, "tb:done"},       // b finished; a answered, not "done"
		{map[string]string{"a": "asking", "b": "idle"}, "ta:asking"},   // a waits again
		{map[string]string{"a": "asking", "b": "exited"}, ""},          // exiting is not finishing
		{map[string]string{"a": "asking", "c": "asking"}, "tc:asking"}, // a new session that already waits
		{map[string]string{"a": "working", "c": "asking"}, ""},         //
		{map[string]string{"a": "idle", "c": ""}, "ta:done"},           // status unknown: nothing
	}
	for i, s := range steps {
		if got := kinds(w.Changes(snap(s.st))); got != s.want {
			t.Errorf("step %d: %q, want %q", i, got, s.want)
		}
	}
}

func TestOnlySelectedKinds(t *testing.T) {
	w := &Watcher{On: map[Kind]bool{Asking: true}}
	w.Changes(snap(map[string]string{"a": "working"}))
	if got := kinds(w.Changes(snap(map[string]string{"a": "idle"}))); got != "" {
		t.Errorf("done reported although only asking was asked for: %q", got)
	}
}

func TestNewAisleSessionWithoutConversation(t *testing.T) {
	w := &Watcher{On: map[Kind]bool{Asking: true}}
	w.Changes(snap(nil))
	evs := w.Changes(snap(nil, session.Runtime{Name: "codex-web", Engine: "codex", Path: "/w/web", Status: "asking"},
		session.Runtime{Name: "foreign", Status: "asking"})) // unmanaged: no engine, never observed
	if kinds(evs) != "codex-web:asking" || evs[0].Title() != "codex needs you" || evs[0].Body() != "web · codex-web" {
		t.Errorf("got %+v", evs)
	}
}

// Text from conversations reaches osascript only as arguments.
func TestOsascriptTakesTextAsArguments(t *testing.T) {
	e := Event{Kind: Asking, Engine: "claude", Project: "/w/api", Summary: `say "hi" & do shell script "rm -rf ~"`}
	var got []string
	c := Command{Argv: osascriptArgv, run: func(name string, args ...string) error { got = append([]string{name}, args...); return nil }}
	if err := c.Notify(e); err != nil {
		t.Fatal(err)
	}
	script := strings.Join(got[:len(got)-2], " ")
	if strings.Contains(script, "rm -rf") || got[len(got)-1] != e.Body() || got[len(got)-2] != "claude needs you" {
		t.Errorf("argv %q", got)
	}
	if !strings.Contains(script, "Glass") {
		t.Error("waiting agents should play a sound")
	}
	if a := osascriptArgv(Event{Kind: Done, Engine: "-x"}); a[len(a)-2] == "-x finished" || strings.Contains(fmt.Sprint(a), "Glass") {
		t.Errorf("argv %q", a)
	}
}
