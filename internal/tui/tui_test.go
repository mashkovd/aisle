package tui

import "testing"

func TestWordFilterKeepsOrderAndNeedsAllWords(t *testing.T) {
	targets := []string{
		"c1 claude /work/api Deploy pipeline fix",
		"x1 codex /work/web add dark mode",
		"g1 gemini /work/api explain the deploy",
		"a1 agy /work/java Assess Java longevity",
	}
	got := WordFilter("deploy api", targets)
	if len(got) != 2 || got[0].Index != 0 || got[1].Index != 2 {
		t.Fatalf("got %+v", got)
	}
	if len(WordFilter("zzz", targets)) != 0 {
		t.Fatal("no fuzzy matches expected")
	}
	if n := len(WordFilter("", targets)); n != 4 {
		t.Fatalf("empty term keeps everything, got %d", n)
	}
}
