// Package agy discovers Antigravity CLI conversations from its SQLite index
// ~/.gemini/antigravity-cli/conversation_summaries.db (opened read-only).
package agy

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go: no cgo, cross-compiles

	"github.com/mashkovd/aisle/internal/adapter"
	"github.com/mashkovd/aisle/internal/session"
)

const (
	name   = "agy"
	format = "agy-conversation-summaries-sqlite"
)

type Adapter struct{ opts adapter.Options }

func New(opts adapter.Options) *Adapter { return &Adapter{opts: opts} }

func (a *Adapter) Name() string               { return name }
func (a *Adapter) Letter() string             { return "a" }
func (a *Adapter) Capabilities() adapter.Caps { return adapter.Caps{ResumeByID: true} }

func (a *Adapter) dbPath() string {
	return filepath.Join(a.opts.Home, ".gemini", "antigravity-cli", "conversation_summaries.db")
}

func (a *Adapter) Detect(ctx context.Context, withVersion bool) adapter.Detection {
	bin, ver := adapter.DetectBinary(ctx, "agy", withVersion)
	return adapter.Detection{Installed: bin != "", Binary: bin, Version: ver, Storage: []string{a.dbPath()}}
}

func (a *Adapter) Resume(s session.Session) adapter.Command {
	return adapter.Command{Argv: []string{"agy", "--conversation", s.NativeID}, Dir: s.Project}
}

func (a *Adapter) New(dir string) adapter.Command {
	return adapter.Command{Argv: []string{"agy"}, Dir: dir}
}

// sqlite datetime text as agy writes it
var timeLayouts = []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano, "2006-01-02 15:04:05"}

func parseTime(s string) time.Time {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil && t.Year() > 1 {
			return t
		}
	}
	return time.Time{}
}

func (a *Adapter) Discover(ctx context.Context) ([]session.Session, []session.Warning) {
	path := a.dbPath()
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	warn := func(msg string) []session.Warning {
		return []session.Warning{{Adapter: name, Path: path, Message: msg}}
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, warn(err.Error())
	}
	defer db.Close()

	// nesting_depth > 0 are subagent conversations
	rows, err := db.QueryContext(ctx, `
		SELECT conversation_id, title, preview, CAST(last_modified_time AS TEXT),
		       CAST(last_user_input_time AS TEXT), workspace_uris
		FROM conversation_summaries
		-- step_count = 0: opened and never used
		WHERE nesting_depth = 0 AND parent_conversation_id = '' AND step_count > 0
		ORDER BY last_modified_time DESC`)
	if err != nil {
		return nil, warn("unsupported format: " + err.Error())
	}
	defer rows.Close()

	var out []session.Session
	for rows.Next() {
		var id, title, preview, modified, input, uris string
		if err := rows.Scan(&id, &title, &preview, &modified, &input, &uris); err != nil {
			return out, warn("unsupported format: " + err.Error())
		}
		label := title
		if strings.TrimSpace(label) == "" {
			label = preview
		}
		// agy writes 0001-01-01 when it never recorded a time
		updated := parseTime(modified)
		if t := parseTime(input); t.After(updated) {
			updated = t
		}
		out = append(out, session.Session{
			Engine:    name,
			NativeID:  id,
			Project:   workspace(uris),
			Summary:   session.Summarize(label, a.opts.SummaryLen),
			UpdatedAt: updated,
			State:     session.Historical,
			Sources:   []session.Source{{Kind: "conversation", Path: path, Format: format}},
		})
	}
	if err := rows.Err(); err != nil {
		return out, warn(err.Error())
	}
	return out, nil
}

// workspace returns the first file:// workspace URI as a path.
func workspace(uris string) string {
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil || len(list) == 0 {
		return ""
	}
	u, err := url.Parse(list[0])
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}
