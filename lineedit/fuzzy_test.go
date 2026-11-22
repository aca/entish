package lineedit

import "testing"

func TestQuery(t *testing.T) {
	cases := []struct {
		q, s string
		want bool
	}{
		{"", "anything", true},
		{"gst", "git status", true},
		{"gts", "git status", true},
		{"tsg", "git status", false},
		{"'stat", "git status", true},
		{"'sta tus", "git status", true},
		{"^git", "git status", true},
		{"^status", "git status", false},
		{"status$", "git status", true},
		{"^git status$", "git status", true}, // two terms: ^git and status$
		{"^ls$", "ls", true},
		{"^ls$", "ls -la", false},
		{"!log", "git status", true},
		{"!log", "git log", false},
		{"!^git", "make", true},
		{"git !log", "git log", false},
		{"make | ninja", "ninja -C out", true},
		{"make | ninja", "cmake ..", true}, // fuzzy "make" matches "cmake"
		{"'make | 'ninja", "go build", false},
		{"Git", "git status", false}, // smart case
		{"git", "Git Status", true},
	}
	for _, c := range cases {
		if got := ParseQuery(c.q).Match(c.s); got != c.want {
			t.Errorf("%q ~ %q = %v, want %v", c.q, c.s, got, c.want)
		}
	}
}

func TestSearchAndHint(t *testing.T) {
	e := &editor{mode: insertMode, history: []string{"git status", "ls -la", "git log --oneline"}}
	e.buf, e.pos = []rune("git"), 3
	e.startSearch()
	if len(e.search.matches) != 2 || e.search.matches[0] != "git log --oneline" {
		t.Fatalf("matches %q", e.search.matches)
	}
	e.searchKey(18) // older
	e.searchKey('\r')
	if string(e.buf) != "git status" || e.search != nil {
		t.Fatalf("accepted %q", string(e.buf))
	}
	e.startSearch()
	for _, k := range " 'xyz" {
		e.searchKey(k)
	}
	if len(e.search.matches) != 0 {
		t.Fatalf("matches %q", e.search.matches)
	}
	e.searchKey(27)
	if string(e.buf) != "git status" {
		t.Fatalf("Esc did not restore: %q", string(e.buf))
	}

	e.buf, e.pos = []rune("git l"), 5
	e.sug.text = "git log --oneline"
	if h := e.hint(); h != "og --oneline" {
		t.Fatalf("hint %q", h)
	}
	e.insertKey(6) // Ctrl+F
	if string(e.buf) != "git log --oneline" {
		t.Fatalf("Ctrl+F: %q", string(e.buf))
	}
}

func TestStripEscapes(t *testing.T) {
	p := "\x1b]133;A\x07~/src $ \x1b]133;B\x1b\\\x1b[1mx\x1b[0m"
	if got := stripEscapes(p); got != "~/src $ x" {
		t.Errorf("%q", got)
	}
}
