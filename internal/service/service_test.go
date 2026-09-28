package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mashkovd/aisle/internal/search"
	"github.com/mashkovd/aisle/internal/session"
)

// Conversations whose text cannot be indexed are still found by title, after
// text matches; unresumable hits are counted, not listed.
func TestSearchIncludesTitlesAfterTextHits(t *testing.T) {
	ix, err := search.Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	snap := Snapshot{Sessions: []session.Session{
		{Engine: "agy", NativeID: "a1", Summary: "Deploy preview environments"},
		{Engine: "claude", NativeID: "c1", Summary: "Unrelated title"},
	}}
	s := &Service{}
	rs, orphans, err := s.Search(context.Background(), ix, snap, "deploy", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Session.NativeID != "a1" || rs[0].Hit.Role != "title" || orphans != 0 {
		t.Fatalf("got %+v orphans=%d", rs, orphans)
	}
	if rs, _, _ := s.Search(context.Background(), ix, snap, "deploy", "claude"); len(rs) != 0 {
		t.Fatalf("engine filter ignored for titles: %+v", rs)
	}
}
