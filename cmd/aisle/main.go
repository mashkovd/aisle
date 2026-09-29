// Command aisle is a cross-agent session navigator: it discovers the AI coding
// sessions you already have and resumes them in tmux.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/adapter/agy"
	"github.com/mashkovd/aisle/internal/adapter/claude"
	"github.com/mashkovd/aisle/internal/adapter/codex"
	"github.com/mashkovd/aisle/internal/adapter/gemini"
	"github.com/mashkovd/aisle/internal/config"
	"github.com/mashkovd/aisle/internal/runtime/tmux"
	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
	"github.com/mashkovd/aisle/internal/tui"
	"github.com/mashkovd/aisle/internal/worktree"
)

// set by GoReleaser
var (
	version = "dev"
	commit  = ""
	date    = ""
)

var debug bool

type app struct {
	cfg config.Config
	svc *service.Service
}

func newApp() (*app, error) {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", config.Path(), err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	opts := adapter.Options{Home: home, IgnorePrompts: map[string]bool{}, SummaryLen: cfg.SummaryLength}
	for _, p := range cfg.IgnorePrompts {
		opts.IgnorePrompts[strings.ToLower(p)] = true
	}
	available := map[string]adapter.Adapter{
		"claude": claude.New(opts),
		"codex":  codex.New(opts),
		"gemini": gemini.New(opts, cfg.ProjectRoots),
		"agy":    agy.New(opts),
	}
	svc := &service.Service{Tmux: tmux.NewClient()}
	for _, name := range cfg.Adapters {
		a, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("config: unknown adapter %q", name)
		}
		svc.Adapters = append(svc.Adapters, a)
	}
	return &app{cfg: cfg, svc: svc}, nil
}

func main() {
	var a *app
	root := &cobra.Command{
		Use:           "aisle",
		Short:         "Cross-agent session navigator for Claude Code, Codex, Gemini CLI and Antigravity",
		Long:          "aisle discovers the AI coding sessions you already have — whichever agent created them —\nand lets you search, resume and manage them from one place.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// commands that must work even with a broken config
			for c := cmd; c != nil; c = c.Parent() {
				switch c.Name() {
				case "version", "completion", "help", "rules", cobra.ShellCompRequestCmd:
					return nil
				}
			}
			var err error
			a, err = newApp()
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isatty.IsTerminal(os.Stdout.Fd()) {
				return a.list(cmd.Context(), listOpts{})
			}
			return a.interactive(cmd.Context())
		},
	}
	root.PersistentFlags().BoolVar(&debug, "debug", false, "print discovery warnings")

	var lo listOpts
	list := &cobra.Command{
		Use:   "list",
		Short: "List discovered sessions, newest first",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.list(cmd.Context(), lo) },
	}
	list.Flags().BoolVar(&lo.json, "json", false, "machine-readable output")
	list.Flags().StringVar(&lo.engine, "engine", "", "only this engine")
	list.Flags().StringVar(&lo.project, "project", "", "only projects whose path contains this")
	list.Flags().IntVarP(&lo.limit, "limit", "n", 0, "at most this many sessions")

	resume := &cobra.Command{
		Use:   "resume <id-prefix | engine:id-prefix>",
		Short: "Resume a session in tmux (attaches if it is already running)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			snap := a.svc.Discover(cmd.Context())
			s, err := session.Resolve(snap.Sessions, args[0])
			if err != nil {
				return err
			}
			return a.svc.Resume(s)
		},
	}

	var wtName string
	newCmd := &cobra.Command{
		Use:   "new <engine> [dir]",
		Short: "Start a new session of an engine in tmux",
		Long: "Start a new session of an engine in tmux.\n\n" +
			"With --worktree the agent starts in a new git worktree, <repo>/.worktrees/<name> on\n" +
			"branch aisle/<name> from HEAD, so parallel sessions do not edit the same files.\n" +
			"Uncommitted changes stay where they are. Remove it with `git worktree remove` when done.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := os.Getwd()
			if len(args) == 2 {
				dir = args[1]
			}
			o := service.NewOptions{}
			if cmd.Flags().Changed("worktree") {
				o = a.worktreeOpts(wtName)
			}
			return a.svc.New(args[0], dir, o)
		},
	}
	newCmd.Flags().StringVar(&wtName, "worktree", "", "start in a new git worktree (optionally named: --worktree=fix-login)")
	newCmd.Flags().Lookup("worktree").NoOptDefVal = " "

	ver := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "aisle %s", version)
			if commit != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " (%s, %s)", commit, date)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		},
	}

	root.AddCommand(list, resume, newCmd, ver, doctorCmd(&a), searchCmd(&a), indexCmd(&a), rulesCmd())
	if err := root.ExecuteContext(context.Background()); err != nil {
		var code exitError
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "aisle:", err)
		os.Exit(1)
	}
}

func (a *app) worktreeOpts(name string) service.NewOptions {
	return service.NewOptions{Worktree: true, Name: strings.TrimSpace(name), Created: func(wt worktree.Worktree) {
		home, _ := os.UserHomeDir()
		fmt.Fprintf(os.Stderr, "worktree %s on branch %s\n", tilde(wt.Root, home), wt.Branch)
	}}
}

func (a *app) warn(ws []session.Warning) {
	if debug {
		for _, w := range ws {
			level := "warning"
			if w.Info {
				level = "info"
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", level, w)
		}
		return
	}
	if n := session.CountActionable(ws); n > 0 {
		fmt.Fprintf(os.Stderr, "%d discovery warning(s); run with --debug or `aisle doctor`\n", n)
	}
}

type listOpts struct {
	json            bool
	engine, project string
	limit           int
}

// observe fills runtime statuses; a pane counts as working when it changes,
// so a one-shot command looks twice.
func (a *app) observe(snap *service.Snapshot) {
	if !snap.Live() {
		return
	}
	a.svc.Observe(snap)
	time.Sleep(500 * time.Millisecond)
	a.svc.Observe(snap)
}

func (a *app) list(ctx context.Context, o listOpts) error {
	snap := a.svc.Discover(ctx)
	a.warn(snap.Warnings)
	a.observe(&snap)
	var ss []session.Session
	for _, s := range snap.Sessions {
		if o.engine != "" && s.Engine != o.engine {
			continue
		}
		if o.project != "" && !strings.Contains(s.Project, o.project) {
			continue
		}
		ss = append(ss, s)
		if o.limit > 0 && len(ss) == o.limit {
			break
		}
	}
	if o.json {
		snap.Sessions = ss
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}
	now := time.Now()
	home, _ := os.UserHomeDir()
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ENGINE\tID\tPROJECT\tUPDATED\tLIVE\tSUMMARY")
	for _, s := range ss {
		live := ""
		if s.Runtime != nil {
			live = s.Runtime.Name
			if s.Runtime.Status != "" {
				live += " (" + s.Runtime.Status + ")"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Engine, short(s.NativeID), tilde(s.Project, home), session.Ago(s.UpdatedAt, now), live, s.Summary)
	}
	return w.Flush()
}

// tilde abbreviates paths under home for display.
func tilde(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (a *app) interactive(ctx context.Context) error {
	snap := a.svc.Discover(ctx)
	fn, closeIx := a.lazySearchFunc(snap)
	defer closeIx()
	return a.runTUI(ctx, snap, tui.Options{Search: fn})
}

func (a *app) runTUI(ctx context.Context, snap service.Snapshot, opts tui.Options) error {
	opts.Refresh = func(ctx context.Context, cur service.Snapshot, full bool) service.Snapshot {
		var next service.Snapshot
		if full {
			next = a.svc.Discover(ctx)
		} else {
			next = a.svc.Relink(ctx, cur)
		}
		a.svc.Observe(&next)
		return next
	}
	var engines []tui.Engine
	for _, ad := range a.svc.Adapters {
		engines = append(engines, tui.Engine{Name: ad.Name(), Letter: ad.Letter()})
	}
	act, err := tui.Run(ctx, snap, engines, opts)
	if err != nil {
		return err
	}
	switch act.Kind {
	case tui.Resume:
		return a.svc.Resume(act.Session)
	case tui.Attach:
		return a.svc.Attach(act.Runtime)
	case tui.New:
		dir, _ := os.Getwd()
		o := service.NewOptions{}
		if act.Worktree {
			o = a.worktreeOpts("")
		}
		return a.svc.New(act.Engine, dir, o)
	}
	return nil
}
