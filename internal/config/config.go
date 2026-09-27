// Package config loads ~/.config/aisle/config.toml. A missing file means defaults.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// Adapters lists the enabled engines, in display order.
	Adapters []string `toml:"adapters"`
	// ProjectRoots are searched to resolve a project name to a directory
	// when an engine stores only the name (legacy Gemini logs).
	ProjectRoots []string `toml:"project_roots"`
	// IgnorePrompts are never used as a session summary (case-insensitive).
	IgnorePrompts []string `toml:"ignore_prompts"`
	SummaryLength int      `toml:"summary_length"`
}

func Default() Config {
	return Config{
		Adapters:      []string{"claude", "codex", "gemini", "agy"},
		ProjectRoots:  []string{"~/Projects", "~/src", "~/code", "~/PycharmProjects", "~/IdeaProjects", "~/GolandProjects"},
		IgnorePrompts: []string{"yes", "no", "ok", "y", "n", "continue", "/clear", "/compact", "/doctor", "/exit", "/quit", "/model", "да", "нет", "ок", "отмена", "отчет", "отчёт"},
		SummaryLength: 80,
	}
}

// Path honours $AISLE_CONFIG, then $XDG_CONFIG_HOME.
func Path() string {
	if p := os.Getenv("AISLE_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "aisle", "config.toml")
}

// Load reads the config at path; fields left out keep their defaults.
func Load(path string) (Config, error) {
	c := Default()
	if _, err := toml.DecodeFile(path, &c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	return c, nil
}

// Expand resolves a leading ~ against home.
func Expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
