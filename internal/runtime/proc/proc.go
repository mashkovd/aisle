// Package proc reads the process table, to find what runs inside a tmux pane.
package proc

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

type Process struct {
	PID, PPID int
	Argv      []string // split on spaces; agent arguments that matter (IDs, flags) have none
}

// Table is a snapshot of the process table.
type Table struct {
	byPID    map[int]Process
	children map[int][]Process
}

// Read lists every process via ps.
func Read(ctx context.Context) (Table, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-ww", "-o", "pid=,ppid=,args=").Output()
	if err != nil {
		return Table{}, err
	}
	return Parse(string(out)), nil
}

// Parse reads `ps -o pid=,ppid=,args=` output.
func Parse(out string) Table {
	t := Table{byPID: map[int]Process{}, children: map[int][]Process{}}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		p := Process{PID: pid, PPID: ppid, Argv: f[2:]}
		t.byPID[pid] = p
		t.children[ppid] = append(t.children[ppid], p)
	}
	return t
}

// Tree returns pid itself and every process below it, nearest first.
func (t Table) Tree(pid int) []Process {
	var out []Process
	if p, ok := t.byPID[pid]; ok {
		out = append(out, p)
	}
	queue := []int{pid}
	seen := map[int]bool{pid: true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range t.children[p] {
			if !seen[c.PID] {
				seen[c.PID] = true
				out = append(out, c)
				queue = append(queue, c.PID)
			}
		}
	}
	return out
}
