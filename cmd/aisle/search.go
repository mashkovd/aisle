package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/mashkovd/aisle/internal/search"
	"github.com/mashkovd/aisle/internal/service"
	"github.com/mashkovd/aisle/internal/session"
	"github.com/mashkovd/aisle/internal/tui"
)

// openIndex opens the index and brings it up to date, reporting progress on
// stderr when the update takes a noticeable time.
func (a *app) openIndex(ctx context.Context) (*search.Index, []session.Warning, error) {
	ix, err := search.Open(search.DefaultPath())
	if err != nil {
		return nil, nil, err
	}
	start := time.Now()
	tty := isatty.IsTerminal(os.Stderr.Fd())
	shown := false
	st, warns := ix.Update(ctx, a.svc.Sources(), func(done, total int) {
		if tty && total > 20 && time.Since(start) > 700*time.Millisecond {
			shown = true
			fmt.Fprintf(os.Stderr, "\rindexing conversations… %d/%d", done, total)
		}
	})
	if shown {
		fmt.Fprintf(os.Stderr, "\rindexed %d conversation file(s), %d message(s) in %s\n", st.Changed, st.Messages, time.Since(start).Round(100*time.Millisecond))
	}
	return ix, warns, nil
}

type searchOpts struct {
	json            bool
	engine, project string
	limit           int
	recent          bool
}

func searchCmd(ap **app) *cobra.Command {
	var o searchOpts
	cmd := &cobra.Command{
		Use:   "search <words…>",
		Short: "Full-text search across every agent's conversations",
		Long: `Search the text of your conversations — prompts and replies — across all agents.
Every word must match; the last word also matches as a prefix.
In a terminal the results open in the navigator, ready to resume.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := *ap
			ctx := cmd.Context()
			query := strings.Join(args, " ")
			ix, iwarns, err := a.openIndex(ctx)
			if err != nil {
				return err
			}
			defer ix.Close()
			snap := a.svc.Discover(ctx)
			snap.Warnings = append(snap.Warnings, iwarns...)
			results, orphans, err := a.svc.Search(ctx, ix, snap, query, o.engine)
			if err != nil {
				return err
			}
			results = filterResults(results, o)
			if !o.json && isatty.IsTerminal(os.Stdout.Fd()) {
				return a.runTUI(ctx, snap, tui.Options{Query: query, Results: results, Search: a.searchFunc(ix, snap)})
			}
			a.warn(snap.Warnings)
			if orphans > 0 && debug {
				fmt.Fprintf(os.Stderr, "info: %d matching conversation(s) cannot be resumed and are not listed\n", orphans)
			}
			if o.json {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				for i := range results {
					results[i].Hit.Snippet = plainSnippet(results[i].Hit.Snippet, "", "")
				}
				return enc.Encode(results)
			}
			printResults(results)
			return nil
		},
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "machine-readable output")
	cmd.Flags().StringVar(&o.engine, "engine", "", "only this engine")
	cmd.Flags().StringVar(&o.project, "project", "", "only projects whose path contains this")
	cmd.Flags().IntVarP(&o.limit, "limit", "n", 20, "at most this many sessions (0 = all)")
	cmd.Flags().BoolVar(&o.recent, "recent", false, "order by last activity instead of relevance")
	return cmd
}

func filterResults(rs []service.Result, o searchOpts) []service.Result {
	var out []service.Result
	for _, r := range rs {
		if o.project != "" && !strings.Contains(r.Session.Project, o.project) {
			continue
		}
		out = append(out, r)
	}
	if o.recent {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Session.UpdatedAt.After(out[j].Session.UpdatedAt) })
	}
	if o.limit > 0 && len(out) > o.limit {
		out = out[:o.limit]
	}
	return out
}

// plainSnippet replaces the index's highlight markers.
func plainSnippet(s, start, end string) string {
	return strings.NewReplacer(search.MarkStart, start, search.MarkEnd, end).Replace(s)
}

func printResults(rs []service.Result) {
	now := time.Now()
	home, _ := os.UserHomeDir()
	bold, reset := "", ""
	if isatty.IsTerminal(os.Stdout.Fd()) {
		bold, reset = "\x1b[1m", "\x1b[0m"
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ENGINE\tID\tPROJECT\tUPDATED\tHITS\tMATCH")
	for _, r := range rs {
		s := r.Session
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", s.Engine, short(s.NativeID), tilde(s.Project, home),
			session.Ago(s.UpdatedAt, now), r.Hit.Matches, plainSnippet(r.Hit.Snippet, bold, reset))
	}
	_ = w.Flush()
}

func indexCmd(ap **app) *cobra.Command {
	var rebuild, purge bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Update the local full-text index (or --rebuild / --purge it)",
		Long: `aisle keeps a full-text index of your conversations at ` + "`" + `aisle doctor` + "`" + `'s index path.
It holds copies of your prompts, so it is private to your user (0600).
It is only a cache: --purge deletes it and the next search rebuilds it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := search.DefaultPath()
			if purge || rebuild {
				if err := search.Purge(path); err != nil {
					return err
				}
				if purge {
					fmt.Println("removed", path)
					return nil
				}
			}
			a := *ap
			ix, warns, err := a.openIndex(cmd.Context())
			if err != nil {
				return err
			}
			defer ix.Close()
			a.warn(warns)
			st, err := ix.Stats(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("%s: %d file(s), %d message(s), %.1f MB\n", st.Path, st.Files, st.Messages, float64(st.Bytes)/1e6)
			return nil
		},
	}
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "delete and rebuild the index")
	cmd.Flags().BoolVar(&purge, "purge", false, "delete the index")
	return cmd
}

// searchFunc gives the TUI a search that reuses an open, up-to-date index.
func (a *app) searchFunc(ix *search.Index, snap service.Snapshot) tui.SearchFunc {
	return func(ctx context.Context, query string) ([]service.Result, error) {
		rs, _, err := a.svc.Search(ctx, ix, snap, query, "")
		return rs, err
	}
}

// lazySearchFunc opens and updates the index on the first search only, so
// plain ` + "`aisle`" + ` starts instantly.
func (a *app) lazySearchFunc(snap service.Snapshot) (tui.SearchFunc, func()) {
	var ix *search.Index
	fn := func(ctx context.Context, query string) ([]service.Result, error) {
		if ix == nil {
			var err error
			if ix, err = search.Open(search.DefaultPath()); err != nil {
				return nil, err
			}
			ix.Update(ctx, a.svc.Sources(), nil)
		}
		rs, _, err := a.svc.Search(ctx, ix, snap, query, "")
		return rs, err
	}
	return fn, func() {
		if ix != nil {
			ix.Close()
		}
	}
}
