package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mashkovd/aisle/internal/notify"
	"github.com/mashkovd/aisle/internal/service"
)

func watchCmd(a **app) *cobra.Command {
	var (
		on       string
		interval time.Duration
	)
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Notify when a running agent starts waiting for you or finishes",
		Long: "aisle watch keeps looking at the agents running in tmux and sends a desktop\n" +
			"notification when one starts waiting for you (a permission prompt or question)\n" +
			"or finishes its work. It runs in the foreground; leave it in a tmux window.\n" +
			"States found at start are not reported, only changes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := &notify.Watcher{On: map[notify.Kind]bool{}}
			for _, k := range strings.Split(on, ",") {
				switch k = strings.TrimSpace(k); notify.Kind(k) {
				case notify.Asking, notify.Done:
					w.On[notify.Kind(k)] = true
				default:
					return fmt.Errorf("--on: unknown event %q (asking, done)", k)
				}
			}
			if interval < 500*time.Millisecond {
				return fmt.Errorf("--interval must be at least 500ms")
			}
			n, err := notify.System()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return (*a).watch(ctx, w, n, interval)
		},
	}
	cmd.Flags().StringVar(&on, "on", "asking,done", "events to notify about: asking, done")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "how often to look")
	return cmd
}

// rediscoverEvery matches the navigator: conversations are read again now
// and then, tmux on every look.
const rediscoverEvery = 15 * time.Second

func (a *app) watch(ctx context.Context, w *notify.Watcher, n notify.Notifier, interval time.Duration) error {
	if !a.svc.Tmux.Available() {
		return fmt.Errorf("tmux is not installed")
	}
	snap := a.svc.Discover(ctx)
	a.svc.Observe(&snap)
	w.Changes(snap)
	last := time.Now()
	fmt.Fprintf(os.Stderr, "watching %d live session(s); ctrl-c to stop\n", live(snap))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		if time.Since(last) >= rediscoverEvery {
			snap, last = a.svc.Discover(ctx), time.Now()
		} else {
			snap = a.svc.Relink(ctx, snap)
		}
		a.svc.Observe(&snap)
		for _, e := range w.Changes(snap) {
			mark := "⚑"
			if e.Kind == notify.Done {
				mark = "✓"
			}
			fmt.Printf("%s %s %s — %s (%s)\n", time.Now().Format("15:04:05"), mark, e.Title(), e.Body(), e.Runtime)
			if err := n.Notify(e); err != nil {
				fmt.Fprintf(os.Stderr, "notification failed: %v\n", err)
			}
		}
	}
}

func live(snap service.Snapshot) int {
	n := 0
	for _, s := range snap.Sessions {
		if s.Runtime != nil {
			n++
		}
	}
	for _, rt := range snap.Unlinked {
		if rt.Engine != "" {
			n++
		}
	}
	return n
}
