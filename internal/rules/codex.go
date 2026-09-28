package rules

import (
	"fmt"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Codex:
//   - reads AGENTS.override.md, else AGENTS.md, else the configured
//     project_doc_fallback_filenames, in each directory from the git root
//     down to the working directory, plus ~/.codex/AGENTS(.override).md;
//   - has no includes: `@path` is plain text;
//   - stops reading project docs after project_doc_max_bytes (32 KiB).
const codexMaxBytes = 32 << 10

type codexConfig struct {
	MaxBytes  int64    `toml:"project_doc_max_bytes"`
	Fallbacks []string `toml:"project_doc_fallback_filenames"`
}

func readCodexConfig(dir string) codexConfig {
	c := codexConfig{MaxBytes: codexMaxBytes}
	// only these two keys are decoded; the rest of config.toml is not read
	_, _ = toml.DecodeFile(filepath.Join(dir, "config.toml"), &c)
	if c.MaxBytes <= 0 {
		c.MaxBytes = codexMaxBytes
	}
	return c
}

func codexProject(e Env, r *Report) {
	cfg := readCodexConfig(e.CodexDir)
	codexDir(r, r.Root, cfg, true)
}

func codexGlobal(e Env, r *Report) {
	codexDir(r, e.CodexDir, codexConfig{MaxBytes: codexMaxBytes}, false)
}

func codexDir(r *Report, dir string, cfg codexConfig, project bool) {
	ar := AgentReport{Agent: "codex", Reach: Missing}
	defer func() { r.Agents = append(r.Agents, ar) }()
	names := []string{"AGENTS.override.md", "AGENTS.md"}
	if project {
		names = append(names, cfg.Fallbacks...)
	}
	found := existing(dir, names...)
	if len(found) == 0 {
		return
	}
	loaded := found[0]
	ar.Files = []string{loaded}
	if sameFile(loaded, r.Source) {
		ar.Reach = Native
	} else if filepath.Base(loaded) == "AGENTS.override.md" && r.Exists {
		r.add(Finding{Agent: "codex", Level: Warn, Code: "codex-override-shadows", File: loaded,
			Message: "Codex reads AGENTS.override.md instead of AGENTS.md, so AGENTS.md is ignored while it exists"})
	}
	if n := size(loaded); n > cfg.MaxBytes {
		f := Finding{Agent: "codex", Level: Warn, Code: "codex-size-limit", File: loaded,
			Message: fmt.Sprintf("%d bytes; Codex reads only the first %d (project_doc_max_bytes)", n, cfg.MaxBytes)}
		if !project {
			f.Level, f.Unverified = Info, true
			f.Message = fmt.Sprintf("%d bytes; the %d-byte project_doc_max_bytes limit may also apply to the global file", n, cfg.MaxBytes)
		}
		r.add(f)
	}
}
