package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type OpKind string

const (
	OpCreate        OpKind = "create"         // write Content to a file that does not exist
	OpAddImport     OpKind = "add-import"     // append the include line Text
	OpReplaceImport OpKind = "replace-import" // replace include Old with New on line Line
	OpGeminiMigrate OpKind = "gemini-migrate" // move contextFileName to context.fileName
	OpGeminiNames   OpKind = "gemini-names"   // set context.fileName to Names
	OpMove          OpKind = "move"           // rename Path to To (init --from)
)

// Op is one file edit. Ops never discard what the user wrote: they add a
// line, rewrite one include, move a settings key, or create a file that
// does not exist yet.
type Op struct {
	Kind  OpKind   `json:"kind"`
	Path  string   `json:"path"`
	To    string   `json:"to,omitempty"`
	Text  string   `json:"text,omitempty"`
	Line  int      `json:"line,omitempty"`
	Old   string   `json:"old,omitempty"`
	New   string   `json:"new,omitempty"`
	Names []string `json:"names,omitempty"`
}

func (o Op) key() string { b, _ := json.Marshal(o); return string(b) }

// Describe renders the op for a plan; settings values are never printed,
// since settings files can hold credentials.
func (o Op) Describe(root, home string) string {
	p := Display(o.Path, root, home)
	switch o.Kind {
	case OpCreate:
		return fmt.Sprintf("create %s", p)
	case OpAddImport:
		return fmt.Sprintf("append %q to %s", o.Text, p)
	case OpReplaceImport:
		return fmt.Sprintf("%s:%d: replace %s with %s", p, o.Line, o.Old, o.New)
	case OpGeminiMigrate:
		return fmt.Sprintf("%s: move the ignored contextFileName to context.fileName (value kept)", p)
	case OpGeminiNames:
		return fmt.Sprintf("%s: set context.fileName to %s", p, strings.Join(o.Names, ", "))
	case OpMove:
		return fmt.Sprintf("rename %s to %s", p, Display(o.To, root, home))
	}
	return string(o.Kind) + " " + p
}

// Safe reports ops allowed on global files: they only add to or move what
// the user wrote.
func (o Op) Safe() bool {
	switch o.Kind {
	case OpAddImport, OpGeminiMigrate:
		return true
	case OpCreate:
		return strings.HasPrefix(strings.TrimSpace(o.Text), "@")
	}
	return false
}

// Result is what applying one op did.
type Result struct {
	Op      Op     `json:"op"`
	Changed bool   `json:"changed"`
	Backup  string `json:"backup,omitempty"`
}

// Apply performs ops in order. Every file that is modified is first copied
// to <file>.aisle-bak (or .aisle-bak.N when that exists); a file whose
// content would not change is left alone, so applying twice is a no-op.
func Apply(ops []Op) ([]Result, error) {
	var out []Result
	for _, o := range ops {
		r, err := apply(o)
		if err != nil {
			return out, fmt.Errorf("%s: %w", o.Path, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func apply(o Op) (Result, error) {
	res := Result{Op: o}
	if o.Kind == OpMove {
		if exists(o.To) {
			return res, fmt.Errorf("%s already exists", o.To)
		}
		if err := os.Rename(o.Path, o.To); err != nil {
			return res, err
		}
		res.Changed = true
		return res, nil
	}

	path := o.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // edit the file a symlink points to, not the link
	}
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	next, err := o.transform(old, existed)
	if err != nil {
		return res, err
	}
	if existed && bytes.Equal(old, next) {
		return res, nil
	}
	mode := fs.FileMode(0o644)
	if o.Kind == OpGeminiNames || o.Kind == OpGeminiMigrate {
		mode = 0o600 // settings files tend to collect credentials
	}
	if existed {
		st, err := os.Stat(path)
		if err != nil {
			return res, err
		}
		mode = st.Mode().Perm()
		if res.Backup, err = backup(path, old, mode); err != nil {
			return res, err
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return res, err
	}
	if err := writeAtomic(path, next, mode); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

func (o Op) transform(old []byte, existed bool) ([]byte, error) {
	switch o.Kind {
	case OpCreate:
		if existed {
			return old, nil // never overwrite
		}
		return []byte(o.Text), nil
	case OpAddImport:
		for _, imp := range scanImports(old, "", "", AtPath) {
			if imp.Token == o.Text {
				return old, nil
			}
		}
		s := string(old)
		switch {
		case s == "":
		case strings.HasSuffix(s, "\n\n"):
		case strings.HasSuffix(s, "\n"):
			s += "\n"
		default:
			s += "\n\n"
		}
		return []byte(s + o.Text + "\n"), nil
	case OpReplaceImport:
		lines := strings.Split(string(old), "\n")
		if o.Line < 1 || o.Line > len(lines) {
			return nil, fmt.Errorf("line %d is gone; run check again", o.Line)
		}
		l := lines[o.Line-1]
		i := indexToken(l, o.Old)
		if i < 0 {
			if indexToken(l, o.New) >= 0 {
				return old, nil
			}
			return nil, fmt.Errorf("line %d no longer contains %s; run check again", o.Line, o.Old)
		}
		lines[o.Line-1] = l[:i] + o.New + l[i+len(o.Old):]
		return []byte(strings.Join(lines, "\n")), nil
	case OpGeminiMigrate, OpGeminiNames:
		return editGeminiSettings(old, existed, o)
	}
	return nil, fmt.Errorf("unknown op %q", o.Kind)
}

// indexToken finds tok in line as a whole word.
func indexToken(line, tok string) int {
	for from := 0; ; {
		i := strings.Index(line[from:], tok)
		if i < 0 {
			return -1
		}
		i += from
		end := i + len(tok)
		before := i == 0 || line[i-1] == ' ' || line[i-1] == '\t'
		rest := strings.TrimLeft(line[end:], ".,;:!?)]}\"'")
		after := rest == "" || rest[0] == ' ' || rest[0] == '\t'
		if before && after {
			return i
		}
		from = i + 1
	}
}

func editGeminiSettings(old []byte, existed bool, o Op) ([]byte, error) {
	doc := newObject()
	if existed && len(bytes.TrimSpace(old)) > 0 {
		var err error
		if doc, err = parseObject(old); err != nil {
			return nil, fmt.Errorf("not plain JSON (comments?), edit it by hand: %w", err)
		}
	}
	ctx := newObject()
	if raw, ok := doc.get("context"); ok {
		var err error
		if ctx, err = parseObject(raw); err != nil {
			return nil, fmt.Errorf("context is not an object: %w", err)
		}
	}
	switch o.Kind {
	case OpGeminiMigrate:
		legacy, ok := doc.get("contextFileName")
		if !ok {
			return old, nil
		}
		if _, set := ctx.get("fileName"); !set {
			ctx.set("fileName", legacy) // context.fileName wins when both are set, so keep it
		}
		doc.del("contextFileName")
	case OpGeminiNames:
		v, _ := json.Marshal(o.Names)
		ctx.set("fileName", v)
	}
	doc.set("context", ctx.marshal(""))
	return append(doc.marshal(""), '\n'), nil
}

func backup(path string, data []byte, mode fs.FileMode) (string, error) {
	b := path + ".aisle-bak"
	for n := 1; exists(b); n++ {
		b = fmt.Sprintf("%s.aisle-bak.%d", path, n)
	}
	return b, writeAtomic(b, data, mode)
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // gone after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// object is a JSON object that keeps its key order, so rewriting a settings
// file changes only the keys aisle touches.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object { return &object{vals: map[string]json.RawMessage{}} }

func parseObject(b []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	o := newObject()
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("trailing data after the object")
	}
	return o, nil
}

func (o *object) get(k string) (json.RawMessage, bool) { v, ok := o.vals[k]; return v, ok }

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) marshal(prefix string) []byte {
	if len(o.keys) == 0 {
		return []byte("{}")
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range o.keys {
		kb, _ := json.Marshal(k)
		b.WriteString(prefix + "  ")
		b.Write(kb)
		b.WriteString(": ")
		var v bytes.Buffer
		if err := json.Indent(&v, o.vals[k], prefix+"  ", "  "); err != nil {
			v.Reset()
			v.Write(o.vals[k])
		}
		b.Write(v.Bytes())
		if i < len(o.keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(prefix + "}")
	return b.Bytes()
}
