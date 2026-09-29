package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mashkovd/aisle/internal/worktree"
)

func worktreeCmd(a **app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "List and remove the git worktrees aisle created for sessions",
	}
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "Worktrees under <repo>/.worktrees: branch, uncommitted changes, unmerged commits, sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, _ := os.Getwd()
			repo, infos, err := worktree.List(dir)
			if err != nil {
				return err
			}
			type row struct {
				worktree.Info
				Sessions []string `json:"sessions,omitempty"`
			}
			rows := make([]row, 0, len(infos))
			for _, i := range infos {
				r := row{Info: i}
				for _, rt := range (*a).svc.RuntimesIn(i.Path) {
					r.Sessions = append(r.Sessions, rt.Name)
				}
				rows = append(rows, r)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			home, _ := os.UserHomeDir()
			if len(rows) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no worktrees in %s/.worktrees (create one with `aisle new <engine> --worktree`)\n", tilde(repo, home))
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tBRANCH\tCHANGES\tUNMERGED\tSESSIONS")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", r.Name, r.Branch, r.Changes, r.Ahead, strings.Join(r.Sessions, ", "))
			}
			return w.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")

	rm := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a worktree; its aisle/ branch too, when merged",
		Long: "Remove a worktree from <repo>/.worktrees. Refuses when it has uncommitted changes or\n" +
			"an agent session still works in it. Its branch aisle/<name> is deleted only when\n" +
			"it is merged into the main worktree's HEAD; otherwise it is kept.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := os.Getwd()
			r, err := (*a).svc.RemoveWorktree(dir, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "removed worktree %s\n", r.Name)
			switch {
			case r.BranchDeleted:
				fmt.Fprintf(out, "deleted branch %s (merged)\n", r.Branch)
			case r.BranchKept != "":
				fmt.Fprintf(out, "kept branch %s: %s\n", r.Branch, r.BranchKept)
			}
			return nil
		},
	}
	rm.ValidArgsFunction = func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		dir, _ := os.Getwd()
		_, infos, err := worktree.List(dir)
		if err != nil || len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, i := range infos {
			names = append(names, i.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
	cmd.AddCommand(list, rm)
	return cmd
}
