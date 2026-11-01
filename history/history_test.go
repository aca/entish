package history

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	a, err := Open(path, func() string { return "/tmp" })
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, c := range []string{"git status", "ls -la", "git status", "git log --oneline"} {
		if err := a.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	got := a.Entries()
	want := []string{"ls -la", "git status", "git log --oneline"}
	if len(got) != len(want) {
		t.Fatalf("entries %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries %q, want %q", got, want)
		}
	}
	if tm, dir, ok := a.Last("ls -la"); !ok || dir != "/tmp" || time.Since(tm) > time.Minute {
		t.Errorf("Last = %v, %q, %v", tm, dir, ok)
	}
	if _, _, ok := a.Last("nope"); ok {
		t.Error("Last of an unknown cmd")
	}
	if s, _ := a.Suggest(context.Background(), "git "); s != "git log --oneline" {
		t.Errorf("suggest %q", s)
	}
	if s, _ := a.Suggest(context.Background(), "git log --oneline"); s != "" {
		t.Errorf("suggest for a full entry %q", s)
	}

	// A second shell on the same database sees the entries, and the first
	// picks up the second's new entry at its next Entries call.
	b, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if n := len(b.Entries()); n != 3 {
		t.Fatalf("second shell sees %d entries", n)
	}
	b.Add("make test")
	if e := a.Entries(); e[len(e)-1] != "make test" {
		t.Errorf("first shell did not pick up the new entry: %q", e)
	}
}
