package lineedit

import (
	"slices"
	"strings"
	"testing"
)

// run feeds keys to an editor holding text with the cursor at pos (in
// normal mode) and returns the resulting text and cursor.
func run(text string, pos int, keys string) (string, int, mode) {
	e := &editor{buf: []rune(text), pos: pos, mode: normalMode}
	e.replay = []rune(keys)
	for len(e.replay) > 0 {
		k := e.next()
		if e.mode == insertMode {
			e.insertKey(k)
		} else {
			e.normalKey(k)
		}
	}
	return string(e.buf), e.pos, e.mode
}

func TestVi(t *testing.T) {
	cases := []struct {
		text string
		pos  int
		keys string
		want string
		wpos int
	}{
		{"git log --oneline", 0, "w", "git log --oneline", 4},
		{"git log --oneline", 0, "2w", "git log --oneline", 8},
		{"git log --oneline", 0, "$", "git log --oneline", 16},
		{"git log --oneline", 16, "b", "git log --oneline", 10},
		{"git log --oneline", 16, "B", "git log --oneline", 8},
		{"git log --oneline", 0, "e", "git log --oneline", 2},
		{"git log --oneline", 0, "fo", "git log --oneline", 5},
		{"git log --oneline", 0, "fo;", "git log --oneline", 10},
		{"git log --oneline", 0, "dw", "log --oneline", 0},
		{"git log --oneline", 4, "cwshow\x1b", "git show --oneline", 7},
		{"git log --oneline", 4, "D", "git ", 3},
		{"git log --oneline", 4, "x", "git og --oneline", 4},
		{"git log --oneline", 0, "3x", " log --oneline", 0},
		{"echo 'a b' c", 7, "ci'z\x1b", "echo 'z' c", 6},
		{"echo 'a b' c", 7, "da'", "echo  c", 5},
		{"f (a (b) c)", 9, "di(", "f ()", 3},
		{"f (a (b) c)", 6, "ci(x\x1b", "f (a (x) c)", 6},
		{"one two", 4, "diw", "one ", 3},
		{"one two", 0, "daw", "two", 0},
		{"a\nb\nc", 2, "dd", "a\nc", 2},
		{"a\nb\nc", 4, "dd", "a\nb", 2},
		{"a\nb\nc", 0, "2dd", "c", 0},
		{"a\nb\nc", 0, "dj", "c", 0},
		{"a\nb\nc", 0, "yyp", "a\na\nb\nc", 2},
		{"a\nb", 2, "yykP", "b\na\nb", 0},
		{"abc", 0, "xp", "bac", 1},
		{"abc", 0, "rz", "zbc", 0},
		{"abc", 0, "3~", "ABC", 2},
		{"abc", 1, "iX\x1b", "aXbc", 1},
		{"abc", 1, "aX\x1b", "abXc", 2},
		{"abc", 1, "AX\x1b", "abcX", 3},
		{"  abc", 4, "IX\x1b", "  Xabc", 2},
		{"a\nb", 0, "J", "a b", 1},
		{"if x {", 0, "obody\x1b", "if x {\nbody", 10},
		{"abc", 0, "xu", "abc", 0},
		{"abc", 0, "xu\x12", "bc", 0},
		{"a b c d", 0, "dw.", "c d", 0},
		{"a b c", 0, "cwX\x1bw.", "X X c", 2},
		{"x", 0, "ahello\x1b", "xhello", 5},
	}
	for _, c := range cases {
		got, pos, _ := run(c.text, c.pos, c.keys)
		if got != c.want || pos != c.wpos {
			t.Errorf("%q @%d %q: got %q @%d, want %q @%d", c.text, c.pos, c.keys, got, pos, c.want, c.wpos)
		}
	}
}

func TestEnterAndIndent(t *testing.T) {
	more := func(s string) bool { // unfinished while braces are open
		n := 0
		for _, r := range s {
			switch r {
			case '{':
				n++
			case '}':
				n--
			}
		}
		return n > 0
	}
	e := &editor{mode: insertMode, more: more}
	e.replay = []rune("if x {\rprint y\r}")
	var act action
	for len(e.replay) > 0 {
		act = e.insertKey(e.next())
	}
	if got, want := string(e.buf), "if x {\n    print y\n}"; got != want {
		t.Errorf("buffer %q, want %q", got, want)
	}
	e.replay = []rune("\r")
	if act = e.insertKey(e.next()); act != actAccept {
		t.Errorf("Enter on a finished input = %v, want accept", act)
	}
}

func TestHistory(t *testing.T) {
	e := &editor{mode: normalMode, history: []string{"one", "two\nlines"}}
	e.hist = append(append([]string(nil), e.history...), "")
	e.hidx = len(e.hist) - 1
	e.replay = []rune("k")
	e.normalKey(e.next())
	if string(e.buf) != "two\nlines" {
		t.Fatalf("k: %q", string(e.buf))
	}
	e.replay = []rune("kk") // first k moves up a line, the second loads older history
	e.normalKey(e.next())
	e.normalKey(e.next())
	if string(e.buf) != "one" {
		t.Fatalf("kk: %q", string(e.buf))
	}
	e.replay = []rune("jj")
	e.normalKey(e.next())
	e.normalKey(e.next())
	if string(e.buf) != "two\nlines" || e.lineIndex(e.pos) != 1 {
		t.Fatalf("jj: %q line %d", string(e.buf), e.lineIndex(e.pos))
	}
}

func TestAbbr(t *testing.T) {
	abbr := func(buf []rune, pos int) (int, string, bool) {
		if pos >= 3 && string(buf[pos-3:pos]) == "gco" {
			return pos - 3, "git checkout", true
		}
		return 0, "", false
	}
	cases := []struct{ keys, want string }{
		{"gco x", "git checkout x"},
		{"gco\x00x", "gco x"}, // Ctrl+Space
		{"gco\r", "git checkout"},
		{"gco\x1b\r", "git checkout"}, // Enter in normal mode
		{"gco \x1bu", ""},             // one undo step
	}
	for _, tc := range cases {
		e := &editor{mode: insertMode, abbr: abbr}
		e.replay = []rune(tc.keys)
		for len(e.replay) > 0 {
			if k := e.next(); e.mode == insertMode {
				e.insertKey(k)
			} else {
				e.normalKey(k)
			}
		}
		if got := string(e.buf); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.keys, got, tc.want)
		}
	}
}

func TestKeyName(t *testing.T) {
	for k, want := range map[rune]string{24: "ctrl-x", 1: "ctrl-a", '\t': "", '\r': "", 'x': "", keyAlt + 'c': "alt-c", keyUp: ""} {
		if got := keyName(k); got != want {
			t.Errorf("keyName(%d) = %q, want %q", k, got, want)
		}
	}
}

func TestSearchRow(t *testing.T) {
	row := searchRow(false, "10-31 19:40", "git status", "~/src/entish", 60)
	if got := strWidth(row); got != 59 {
		t.Errorf("width %d: %q", got, row)
	}
	if !strings.HasSuffix(stripEscapes(row), "git status"+strings.Repeat(" ", 59-2-13-10-12)+"~/src/entish") {
		t.Errorf("layout %q", stripEscapes(row))
	}
	long := searchRow(true, "10-31 19:40", strings.Repeat("x", 100), "/a/very/long/directory/path/here", 60)
	if got := strWidth(long); got != 59 {
		t.Errorf("long row width %d: %q", got, long)
	}
	if !strings.HasSuffix(stripEscapes(long), " …irectory/path/here") {
		t.Errorf("long dir %q", stripEscapes(long))
	}
}

func TestColored(t *testing.T) {
	// the spans are over the whole buffer "ab\ncd|e"; this is its second line
	spans := []Span{{0, 2, "<k>"}, {3, 5, "<x>"}, {5, 6, "<p>"}}
	if got, want := colored([]rune("cd|e"), 3, spans), "<x>cd\x1b[0m<p>|\x1b[0me"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPipeLast(t *testing.T) {
	cases := []struct{ last, text, keys, want string }{
		{"echo 3", "", "| cat", "echo 3 | cat"},
		{"echo 3", "", "|> count", "echo 3 |> count"},
		{"echo 3", "ls ", "|", "ls |"},
		{"", "", "|", "|"},
	}
	for _, c := range cases {
		e := &editor{buf: []rune(c.text), pos: len(c.text), mode: insertMode, last: c.last}
		e.replay = []rune(c.keys)
		for len(e.replay) > 0 {
			e.insertKey(e.next())
		}
		if got := string(e.buf); got != c.want {
			t.Errorf("last %q, %q + %q: got %q, want %q", c.last, c.text, c.keys, got, c.want)
		}
	}
}

func TestOverlay(t *testing.T) {
	spans := []Span{{0, 3, "a"}, {5, 8, "b"}}
	got := overlay(overlay(spans, 1, "x"), 4, "x")
	want := []Span{{0, 1, "a"}, {1, 2, "x"}, {2, 3, "a"}, {4, 5, "x"}, {5, 8, "b"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := overlay(nil, 9, "x"); !slices.Equal(got, []Span{{9, 10, "x"}}) {
		t.Errorf("empty: got %v", got)
	}
}
