package history

import (
	"context"
	"database/sql"
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
	// "git status" ran twice, "git log --oneline" once.
	if s, _ := a.Suggest(context.Background(), "git "); s != "git status" {
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

func TestSuggestContext(t *testing.T) {
	dir := "/a"
	s, err := Open(filepath.Join(t.TempDir(), "h.db"), func() string { return dir })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	add := func(cmds ...string) {
		for _, c := range cmds {
			if err := s.Add(c); err != nil {
				t.Fatal(err)
			}
		}
	}
	suggest := func(buf string) string {
		got, _ := s.Suggest(context.Background(), buf)
		return got
	}

	// In /a "make test" is common; in /b only "make run" was ever used.
	add("make test", "make test", "make test", "ls")
	dir = "/b"
	add("make run", "pwd")
	s.Visit("/a")
	if got := suggest("make "); got != "make test" {
		t.Errorf("in /a: %q", got)
	}
	s.Visit("/b")
	if got := suggest("make "); got != "make run" {
		t.Errorf("in /b: %q", got)
	}

	// "git commit" follows "git add" even though "git checkout" is newer.
	add("git add .", "git commit", "git checkout main", "git add .")
	if got := suggest("git c"); got != "git commit" {
		t.Errorf("after git add: %q", got)
	}
	// With no context to tell them apart, the newer wins.
	add("echo")
	if got := suggest("git c"); got != "git checkout main" {
		t.Errorf("tie: %q", got)
	}
}

func TestMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE history (id INTEGER PRIMARY KEY, cmd TEXT NOT NULL, cwd TEXT NOT NULL DEFAULT '', time INTEGER NOT NULL);
		INSERT INTO history (cmd, cwd, time) VALUES ('old', '/', 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Add("new"); err != nil {
		t.Fatal(err)
	}
	if e := s.Entries(); len(e) != 2 || e[0] != "old" {
		t.Errorf("entries %q", e)
	}
}
