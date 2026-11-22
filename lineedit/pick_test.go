package lineedit

import (
	"slices"
	"strings"
	"testing"
)

func TestPicker(t *testing.T) {
	items := []string{"main", "dev", "feature/x", "fix"}
	cases := []struct {
		keys   string
		picked []int
		intr   bool
	}{
		{"\r", []int{0}, false},
		{"f\x0e\r", []int{3}, false},          // query f matches feature/x, fix; Ctrl+N
		{"\t\t\x10\x10\t\r", []int{1}, false}, // mark 0, 1; back up; unmark 0
		{"\x0e\x0e\x0e\t\x10\x10\x10\t\r", []int{0, 3}, false},
		{"zzz\r\x1b", nil, false}, // Enter with no match does nothing
		{"\x03", nil, true},
	}
	for _, c := range cases {
		p := newPicker(items)
		var done, intr bool
		for _, k := range c.keys {
			if done, intr = p.key(k, ""); done {
				break
			}
		}
		if !done || intr != c.intr || !slices.Equal(p.picked(), c.picked) {
			t.Errorf("%q: done %v intr %v picked %v, want %v %v", c.keys, done, intr, p.picked(), c.picked, c.intr)
		}
	}
}

func TestPickerSmallTerminal(t *testing.T) {
	p := newPicker([]string{"a", "b", "c", "d"})
	p.idx = 3
	got := stripEscapes(p.drawRows(20, 2))
	if strings.Contains(got, "   a") || !strings.Contains(got, ">  d") {
		t.Fatalf("selected item not visible: %q", got)
	}
	p.query = []rune(strings.Repeat("long query", 10))
	for _, cols := range []int{1, 8, 20} {
		got := stripEscapes(p.drawRows(cols, 2))
		lines := strings.Split(got, "\r\n")
		if len(lines) > 3 {
			t.Fatalf("picker exceeds row budget: %q", got)
		}
		for _, line := range lines {
			line = strings.TrimRight(line, "\r")
			if runesWidth([]rune(line)) > max(1, cols-1) {
				t.Errorf("width %d: %q", cols, line)
			}
		}
	}
}
