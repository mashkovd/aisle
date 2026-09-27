package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// Lines is a window of complete JSONL lines from a file.
type Lines struct {
	Raw       [][]byte
	Truncated bool // the window does not cover the whole file
}

// ReadHead returns the complete lines within the first max bytes of path.
func ReadHead(path string, max int64) (Lines, error) {
	f, err := os.Open(path)
	if err != nil {
		return Lines{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Lines{}, err
	}
	buf, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return Lines{}, err
	}
	truncated := st.Size() > int64(len(buf))
	if truncated {
		// drop the trailing partial line
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			buf = buf[:i]
		} else {
			buf = nil
		}
	}
	return Lines{Raw: split(buf), Truncated: truncated}, nil
}

// ReadTail returns the complete lines within the last max bytes of path.
func ReadTail(path string, max int64) (Lines, error) {
	f, err := os.Open(path)
	if err != nil {
		return Lines{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Lines{}, err
	}
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return Lines{}, err
	}
	if off > 0 {
		// the first line started before the window
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}
	return Lines{Raw: split(buf), Truncated: off > 0}, nil
}

// ScanAll calls fn for each non-empty line of path. Lines of any length are
// supported.
func ScanAll(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if l := bytes.TrimSpace(line); len(l) > 0 {
			fn(l)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func split(buf []byte) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(buf, []byte{'\n'}) {
		if l = bytes.TrimSpace(l); len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// Decode unmarshals one JSONL line; it reports false on malformed input.
func Decode(line []byte, v any) bool { return json.Unmarshal(line, v) == nil }
