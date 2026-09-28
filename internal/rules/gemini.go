package rules

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// Gemini CLI:
//   - reads the files named by context.fileName (default GEMINI.md) from
//     ~/.gemini and from the project; the project's .gemini/settings.json
//     overrides the user's;
//   - ignores the top-level contextFileName of older versions (0.46 reads
//     only context.fileName; confirmed in its settings loader);
//   - follows `@path` includes relative to the including file, 5 levels;
//   - ignores a project's settings in a folder the user has not trusted.
const geminiHops = 5

type geminiSettings struct {
	Path     string
	Exists   bool
	Err      error
	Names    []string // context.fileName
	HasNames bool
	Legacy   bool // top-level contextFileName
}

func readGeminiSettings(p string) geminiSettings {
	s := geminiSettings{Path: p}
	data := readFile(p)
	if data == nil {
		return s
	}
	s.Exists = true
	var doc struct {
		Context struct {
			FileName json.RawMessage `json:"fileName"`
		} `json:"context"`
		Legacy json.RawMessage `json:"contextFileName"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		s.Err = err
		return s
	}
	s.Legacy = len(doc.Legacy) > 0
	if n := names(doc.Context.FileName); n != nil {
		s.Names, s.HasNames = n, true
	}
	return s
}

// names decodes a string or an array of strings.
func names(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

func geminiLegacy(e Env, r *Report, s geminiSettings) {
	if s.Err != nil {
		r.add(Finding{Agent: "gemini", Level: Warn, Code: "gemini-settings-unreadable", File: s.Path,
			Message: "not plain JSON, so aisle cannot check it: " + s.Err.Error()})
	}
	if !s.Legacy {
		return
	}
	msg := "top-level contextFileName is ignored by current Gemini CLI, which reads only context.fileName"
	if s.HasNames {
		msg += "; context.fileName is set, so the old key is only dead weight"
	} else {
		msg += ", so Gemini falls back to GEMINI.md alone"
	}
	r.add(Finding{Agent: "gemini", Level: Error, Code: "gemini-legacy-key", File: s.Path, Message: msg,
		Fix: &Op{Kind: OpGeminiMigrate, Path: s.Path}})
}

func geminiProject(e Env, r *Report) {
	user := readGeminiSettings(filepath.Join(e.GeminiDir, "settings.json"))
	proj := readGeminiSettings(filepath.Join(r.Root, ".gemini", "settings.json"))
	geminiLegacy(e, r, user)
	geminiLegacy(e, r, proj)

	fileNames, from := []string{"GEMINI.md"}, "default"
	switch {
	case proj.HasNames:
		fileNames, from = proj.Names, Display(proj.Path, r.Root, e.Home)
	case user.HasNames:
		fileNames, from = user.Names, Display(user.Path, r.Root, e.Home)
	}
	if proj.Exists && !geminiTrusted(e, r.Root) {
		r.add(Finding{Agent: "gemini", Level: Info, Code: "gemini-untrusted", File: proj.Path, Unverified: true,
			Message: "this folder is not in ~/.gemini/trustedFolders.json; Gemini CLI skips a project's settings until you trust it"})
	}

	ar := AgentReport{Agent: "gemini", Reach: Missing, Files: existing(r.Root, fileNames...)}
	defer func() { r.Agents = append(r.Agents, ar) }()
	if !r.Exists {
		return
	}
	if slices.Contains(fileNames, "AGENTS.md") {
		ar.Reach, ar.Via = Config, from
		// another listed file that also carries AGENTS.md loads it twice
		for _, f := range ar.Files {
			if sameFile(f, r.Source) && filepath.Base(f) == "AGENTS.md" {
				continue
			}
			if via, _ := reaches(f, r.Source, e.Home, AtPath, geminiHops); via != "" {
				r.add(Finding{Agent: "gemini", Level: Warn, Code: "gemini-duplicate", File: f, Unverified: true,
					Message: "context.fileName lists AGENTS.md and " + filepath.Base(f) + " also includes it, so Gemini CLI likely loads it twice"})
			}
		}
		return
	}
	for _, f := range ar.Files {
		if via, _ := reaches(f, r.Source, e.Home, AtPath, geminiHops); via != "" {
			ar.Reach, ar.Via = Imported, via
			return
		}
	}
	fd := Finding{Agent: "gemini", Level: Error, Code: "gemini-agents-md-not-loaded", File: proj.Path,
		Message: "Gemini CLI reads " + strings.Join(fileNames, ", ") + " (" + from + ") and none of them is or includes AGENTS.md"}
	if proj.Err == nil {
		fd.Fix = &Op{Kind: OpGeminiNames, Path: proj.Path, Names: append(slices.Clone(fileNames), "AGENTS.md")}
	} else {
		fd.Message += `; add "AGENTS.md" to context.fileName by hand`
	}
	r.add(fd)
}

func geminiGlobal(e Env, r *Report) {
	user := readGeminiSettings(filepath.Join(e.GeminiDir, "settings.json"))
	geminiLegacy(e, r, user)
	fileNames := []string{"GEMINI.md"}
	if user.HasNames {
		fileNames = user.Names
	}
	ar := AgentReport{Agent: "gemini", Reach: Missing, Files: existing(e.GeminiDir, fileNames...)}
	defer func() { r.Agents = append(r.Agents, ar) }()
	for _, f := range ar.Files {
		if via, _ := reaches(f, r.Source, e.Home, AtPath, geminiHops); via != "" {
			ar.Reach, ar.Via = Imported, via
			return
		}
	}
	if r.Exists {
		r.add(Finding{Agent: "gemini", Level: Warn, Code: "gemini-agents-md-not-loaded", File: filepath.Join(e.GeminiDir, "GEMINI.md"),
			Message: "Gemini CLI's global files (" + strings.Join(fileNames, ", ") + " in ~/.gemini) do not include " +
				Display(r.Source, "", e.Home) + globalGeminiHint(e, r)})
	}
}

// globalGeminiHint suggests the one-line fix shared by Gemini CLI and
// Antigravity, which both read ~/.gemini/GEMINI.md.
func globalGeminiHint(e Env, r *Report) string {
	p := filepath.Join(e.GeminiDir, "GEMINI.md")
	if exists(p) {
		return "; include it from " + Display(p, "", e.Home)
	}
	return "; e.g. ln -s " + Display(r.Source, "", e.Home) + " " + Display(p, "", e.Home)
}

// geminiTrusted reads ~/.gemini/trustedFolders.json: TRUST_FOLDER trusts a
// folder and everything below it, TRUST_PARENT the folder's parent.
func geminiTrusted(e Env, dir string) bool {
	var m map[string]string
	if json.Unmarshal(readFile(filepath.Join(e.GeminiDir, "trustedFolders.json")), &m) != nil {
		return false
	}
	for p, v := range m {
		switch v {
		case "TRUST_FOLDER":
			if within(dir, p) {
				return true
			}
		case "TRUST_PARENT":
			if within(dir, filepath.Dir(p)) {
				return true
			}
		}
	}
	return false
}
