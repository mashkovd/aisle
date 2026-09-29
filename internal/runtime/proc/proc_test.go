package proc

import (
	"fmt"
	"testing"
)

func TestTree(t *testing.T) {
	tb := Parse(`  1     0 /sbin/launchd
 10     1 tmux new-session
 11    10 -zsh
 12    11 node /usr/local/bin/gemini --resume g1
 13    12 /bin/sh -c ls
 20     1 unrelated
garbage line
`)
	var got []string
	for _, p := range tb.Tree(11) {
		got = append(got, fmt.Sprintf("%d %s", p.PID, p.Argv[0]))
	}
	if fmt.Sprint(got) != "[11 -zsh 12 node 13 /bin/sh]" {
		t.Errorf("got %v", got)
	}
	if len(tb.Tree(99)) != 0 {
		t.Error("unknown pid has a tree")
	}
}
