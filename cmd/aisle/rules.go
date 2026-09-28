package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/rules"
)

// exitError ends the process with code and no message: `rules check` uses
// it to fail a CI step when it finds problems.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

type rulesOpts struct {
	global bool
	source string
	json   bool
}

func (o *rulesOpts) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.global, "global", false, "check your user-level files instead of a project")
	cmd.Flags().StringVar(&o.source, "source", "", "the global AGENTS.md (default ~/.codex/AGENTS.md, which Codex reads)")
	cmd.Flags().BoolVar(&o.json, "json", false, "machine-readable output")
}

func rulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Keep one AGENTS.md as the instructions every agent reads",
		Long: "aisle rules checks that a project's AGENTS.md reaches Claude Code, Codex, Gemini CLI and\n" +
			"Antigravity, and that no agent reads it twice. It works on the project containing the\n" +
			"current directory (its git root) unless you pass a directory, or on your user-level\n" +
			"files with --global.",
	}

	var co rulesOpts
	check := &cobra.Command{
		Use:   "check [dir]",
		Short: "Report which instruction files each agent loads; exit 1 on problems",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := runCheck(cmd, co, args)
			if err != nil {
				return err
			}
			if co.json {
				return writeJSON(cmd.OutOrStdout(), rep)
			}
			printReport(cmd.OutOrStdout(), rep)
			ops, _ := rules.Plan(rep)
			printSummary(cmd.OutOrStdout(), rep, len(ops))
			if rep.Problems() > 0 {
				return exitError(1)
			}
			return nil
		},
	}
	co.flags(check)

	var so rulesOpts
	var apply bool
	sync := &cobra.Command{
		Use:   "sync [dir]",
		Short: "Print the edits that fix what check finds; --apply makes them",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := runCheck(cmd, so, args)
			if err != nil {
				return err
			}
			return syncReport(cmd, so, args, rep, apply)
		},
	}
	so.flags(sync)
	sync.Flags().BoolVar(&apply, "apply", false, "make the changes (each edited file is backed up to <file>.aisle-bak)")

	var from string
	var dry bool
	initCmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create AGENTS.md (or rename --from CLAUDE.md/GEMINI.md to it) and point every agent at it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			root, err := projectRoot(args)
			if err != nil {
				return err
			}
			ops, err := rules.Init(root, from)
			if err != nil {
				return err
			}
			home, _ := os.UserHomeDir()
			if len(ops) == 0 {
				fmt.Fprintln(out, "AGENTS.md already exists")
			}
			for _, o := range ops {
				if dry {
					fmt.Fprintf(out, "would %s\n", o.Describe(root, home))
					continue
				}
				if _, err := rules.Apply([]rules.Op{o}); err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ %s\n", o.Describe(root, home))
			}
			if dry {
				fmt.Fprintln(out, "then: aisle rules sync --apply")
				return nil
			}
			rep, err := runCheck(cmd, rulesOpts{}, []string{root})
			if err != nil {
				return err
			}
			return syncReport(cmd, rulesOpts{}, []string{root}, rep, true)
		},
	}
	initCmd.Flags().StringVar(&from, "from", "", "rename this file (e.g. CLAUDE.md) to AGENTS.md instead of creating a stub")
	initCmd.Flags().BoolVar(&dry, "dry-run", false, "only print what init would do")

	cmd.AddCommand(check, sync, initCmd)
	return cmd
}

func syncReport(cmd *cobra.Command, o rulesOpts, args []string, rep rules.Report, apply bool) error {
	out := cmd.OutOrStdout()
	home, _ := os.UserHomeDir()
	ops, skipped := rules.Plan(rep)
	if o.json && !apply {
		return writeJSON(out, map[string]any{"report": rep, "plan": ops, "skipped": skipped})
	}
	if len(ops) == 0 {
		fmt.Fprintln(out, "nothing to change")
	}
	var results []rules.Result
	if apply && len(ops) > 0 {
		var err error
		results, err = rules.Apply(ops)
		for _, r := range results {
			switch {
			case r.Backup != "":
				fmt.Fprintf(out, "✓ %s (backup: %s)\n", r.Op.Describe(rep.Root, home), rules.Display(r.Backup, rep.Root, home))
			case r.Changed:
				fmt.Fprintf(out, "✓ %s\n", r.Op.Describe(rep.Root, home))
			default:
				fmt.Fprintf(out, "= %s (already done)\n", r.Op.Describe(rep.Root, home))
			}
		}
		if err != nil {
			return err
		}
	} else {
		for _, op := range ops {
			fmt.Fprintf(out, "• %s\n", op.Describe(rep.Root, home))
		}
	}
	for _, op := range skipped {
		fmt.Fprintf(out, "– not changed outside the project: %s\n", op.Describe(rep.Root, home))
	}
	if !apply {
		if len(ops) > 0 {
			fmt.Fprintln(out, "\nrun with --apply to make these changes (each edited file is backed up to <file>.aisle-bak)")
		}
		return nil
	}
	after, err := runCheck(cmd, o, args)
	if err != nil {
		return err
	}
	if o.json {
		return writeJSON(out, map[string]any{"applied": results, "report": after})
	}
	fmt.Fprintln(out)
	printReport(out, after)
	left, _ := rules.Plan(after)
	printSummary(out, after, len(left))
	return nil
}

func runCheck(cmd *cobra.Command, o rulesOpts, args []string) (rules.Report, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return rules.Report{}, err
	}
	env := rules.DefaultEnv(home, os.Getenv)
	if o.global {
		if len(args) > 0 {
			return rules.Report{}, fmt.Errorf("--global takes no directory")
		}
		src := o.source
		if src == "" {
			src = filepath.Join(env.CodexDir, "AGENTS.md")
		} else if strings.HasPrefix(src, "~/") {
			src = filepath.Join(home, src[2:])
		}
		abs, err := filepath.Abs(src)
		if err != nil {
			return rules.Report{}, err
		}
		return rules.Global(env, abs), nil
	}
	if o.source != "" {
		return rules.Report{}, fmt.Errorf("--source applies to --global only; a project's source is its AGENTS.md")
	}
	root, err := projectRoot(args)
	if err != nil {
		return rules.Report{}, err
	}
	if _, v := adapter.DetectBinary(cmd.Context(), "claude", true); v != "" {
		env.ClaudeVersion = v
	}
	return rules.Project(env, root), nil
}

// projectRoot is the given directory, or the git root of the working
// directory, or the working directory.
func projectRoot(args []string) (string, error) {
	if len(args) > 0 {
		abs, err := filepath.Abs(args[0])
		if err != nil {
			return "", err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return "", fmt.Errorf("%s is not a directory", args[0])
		}
		return abs, nil
	}
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p, nil
		}
	}
	return os.Getwd()
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printReport(w io.Writer, rep rules.Report) {
	home, _ := os.UserHomeDir()
	d := func(p string) string { return rules.Display(p, rep.Root, home) }
	if rep.Scope == "global" {
		fmt.Fprintf(w, "global rules — source %s", tilde(rep.Source, home))
	} else {
		fmt.Fprintf(w, "%s — source AGENTS.md", tilde(rep.Root, home))
	}
	if st, err := os.Stat(rep.Source); err == nil {
		if st.Size() < 1000 {
			fmt.Fprintf(w, " (%d B)", st.Size())
		} else {
			fmt.Fprintf(w, " (%.1f KB)", float64(st.Size())/1000)
		}
	} else {
		fmt.Fprint(w, " (missing)")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
	for _, a := range rep.Agents {
		ok, what := "✗", "not reached"
		switch a.Reach {
		case rules.Native:
			ok, what = "✓", "reads it"
		case rules.Imported:
			ok, what = "✓", "included"
		case rules.Config:
			ok, what = "✓", "configured"
		}
		var detail string
		switch {
		case a.Via == "":
			var fs []string
			for _, f := range a.Files {
				fs = append(fs, d(f))
			}
			detail = strings.Join(fs, ", ")
			if detail == "" {
				detail = "no instruction files"
			}
		case a.Reach == rules.Config:
			detail = "via " + a.Via // already a display path
		default:
			detail = "via " + d(a.Via)
		}
		fmt.Fprintf(w, "  %-7s %s %-12s %s\n", a.Agent, ok, what, detail)
	}
	if len(rep.Findings) > 0 {
		fmt.Fprintln(w)
	}
	for _, f := range rep.Findings {
		mark := map[rules.Level]string{rules.Error: "✗", rules.Warn: "!", rules.Info: "i"}[f.Level]
		loc := d(f.File)
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.Line)
		}
		agent := f.Agent
		if agent == "" {
			agent = "all"
		}
		msg := f.Message
		if f.Unverified {
			msg += " (unverified)"
		}
		fmt.Fprintf(w, "%s %-7s %s\n", mark, agent, loc)
		fmt.Fprintf(w, "          %s\n", msg)
		if f.Fix != nil {
			fmt.Fprintf(w, "          fix: %s\n", f.Fix.Describe(rep.Root, home))
		}
	}
}

func printSummary(w io.Writer, rep rules.Report, fixable int) {
	n := rep.Problems()
	fmt.Fprintln(w)
	switch {
	case n == 0:
		fmt.Fprintln(w, "every agent reads AGENTS.md exactly once")
	case fixable > 0:
		cmd := "aisle rules sync --apply"
		if rep.Scope == "global" {
			cmd = "aisle rules sync --global --apply"
		}
		fmt.Fprintf(w, "%d problem(s); `%s` makes %d safe fix(es)\n", n, cmd, fixable)
	default:
		fmt.Fprintf(w, "%d problem(s); none can be fixed automatically\n", n)
	}
}
