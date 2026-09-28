package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/config"
	"github.com/mashkovd/aisle/internal/session"
)

type storageReport struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type adapterReport struct {
	Name     string            `json:"name"`
	Detect   adapter.Detection `json:"detect"`
	Storage  []storageReport   `json:"storage"`
	Sessions int               `json:"sessions"`
	Live     int               `json:"live"`
	Partial  int               `json:"partial"`
	Formats  map[string]int    `json:"formats"`
	Warnings []string          `json:"warnings,omitempty"`
	Notes    []string          `json:"notes,omitempty"`
}

type doctorReport struct {
	Version  string          `json:"version"`
	Config   storageReport   `json:"config"`
	Tmux     tmuxReport      `json:"tmux"`
	Adapters []adapterReport `json:"adapters"`
}

type tmuxReport struct {
	Binary   string `json:"binary,omitempty"`
	Version  string `json:"version,omitempty"`
	Sessions int    `json:"sessions"`
	Managed  int    `json:"managed"`
	Error    string `json:"error,omitempty"`
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func doctorCmd(a **app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Show installed agents, storage paths, recognised formats and warnings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ap := *a
			ctx := cmd.Context()
			rep := doctorReport{Version: version, Config: storageReport{Path: config.Path(), Exists: exists(config.Path())}}

			if ap.svc.Tmux.Available() {
				rep.Tmux.Binary = ap.svc.Tmux.Bin
				if out, err := exec.CommandContext(ctx, ap.svc.Tmux.Bin, "-V").Output(); err == nil {
					rep.Tmux.Version = strings.TrimSpace(string(out))
				}
				rts, err := ap.svc.Tmux.List()
				if err != nil {
					rep.Tmux.Error = err.Error()
				}
				rep.Tmux.Sessions = len(rts)
				for _, rt := range rts {
					if rt.Managed() {
						rep.Tmux.Managed++
					}
				}
			} else {
				rep.Tmux.Error = "not installed (brew install tmux)"
			}

			snap := ap.svc.Discover(ctx)
			for _, ad := range ap.svc.Adapters {
				r := adapterReport{Name: ad.Name(), Detect: ad.Detect(ctx, true), Formats: map[string]int{}}
				for _, p := range r.Detect.Storage {
					r.Storage = append(r.Storage, storageReport{Path: p, Exists: exists(p)})
				}
				for _, s := range snap.Sessions {
					if s.Engine != ad.Name() {
						continue
					}
					r.Sessions++
					if s.Runtime != nil {
						r.Live++
					}
					if s.Partial() {
						r.Partial++
					}
					for _, src := range s.Sources {
						r.Formats[src.Format]++
					}
				}
				for _, w := range snap.Warnings {
					if w.Adapter != ad.Name() {
						continue
					}
					msg := strings.TrimPrefix(w.String(), ad.Name()+": ")
					if w.Info {
						r.Notes = append(r.Notes, msg)
					} else {
						r.Warnings = append(r.Warnings, msg)
					}
				}
				rep.Adapters = append(rep.Adapters, r)
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			printDoctor(rep, snap.Warnings)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func printDoctor(rep doctorReport, all []session.Warning) {
	fmt.Printf("aisle %s\n", rep.Version)
	if rep.Config.Exists {
		fmt.Printf("config  ✓ %s\n", rep.Config.Path)
	} else {
		fmt.Printf("config  – %s (optional; using defaults)\n", rep.Config.Path)
	}
	if rep.Tmux.Binary != "" {
		fmt.Printf("tmux    ✓ %s (%s) — %d sessions, %d started by aisle\n", rep.Tmux.Version, rep.Tmux.Binary, rep.Tmux.Sessions, rep.Tmux.Managed)
	} else {
		fmt.Printf("tmux    ✗ %s\n", rep.Tmux.Error)
	}
	for _, r := range rep.Adapters {
		fmt.Println()
		if r.Detect.Installed {
			fmt.Printf("%-7s ✓ %s (%s)\n", r.Name, r.Detect.Version, r.Detect.Binary)
		} else {
			fmt.Printf("%-7s ✗ not on PATH\n", r.Name)
		}
		for _, s := range r.Storage {
			fmt.Printf("        %s %s\n", mark(s.Exists), s.Path)
		}
		fmt.Printf("        %d sessions, %d live, %d partial\n", r.Sessions, r.Live, r.Partial)
		formats := make([]string, 0, len(r.Formats))
		for f, n := range r.Formats {
			formats = append(formats, fmt.Sprintf("%s×%d", f, n))
		}
		sort.Strings(formats)
		if len(formats) > 0 {
			fmt.Printf("        formats: %s\n", strings.Join(formats, ", "))
		}
		for _, w := range r.Warnings {
			fmt.Printf("        ! %s\n", w)
		}
		for _, n := range r.Notes {
			fmt.Printf("        i %s\n", n)
		}
	}
	var other []string
	for _, w := range all {
		if w.Adapter == "tmux" {
			other = append(other, w.String())
		}
	}
	for _, w := range other {
		fmt.Printf("\n! %s\n", w)
	}
}
