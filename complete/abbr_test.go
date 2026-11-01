package complete

import (
	"strings"
	"testing"
)

func TestAbbr(t *testing.T) {
	abbrs := map[string]string{"gco": "git checkout", "l": "ls -la"}
	a := Abbr(func(s string) (string, bool) { v, ok := abbrs[s]; return v, ok })
	cases := map[string]string{ // line, with | at the cursor -> result ("" = no expansion)
		"gco|":              "git checkout|",
		"ls | gco|":         "ls | git checkout|",
		"a; l|":             "a; ls -la|",
		"if gco|":           "if git checkout|",
		"gc|o":              "",
		"echo gco|":         "",
		"'gco|":             "",
		"^gco|":             "",
		"print \"a; gco|":   "",
		"print 'a; gco|":    "",
		"ls # x; gco|":      "",
		"\\\\a; gco|":       "",
		"print \"a\"; gco|": "print \"a\"; git checkout|",
		"x\ngco|":           "x\ngit checkout|",
		"gco| x":            "git checkout| x",
	}
	for line, want := range cases {
		i := strings.LastIndexByte(line, '|') // the cursor; pipes come before it
		pos := len([]rune(line[:i]))
		buf := []rune(line[:i] + line[i+1:])
		start, text, ok := a(buf, pos)
		got := ""
		if ok {
			got = string(buf[:start]) + text + "|" + string(buf[pos:])
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}
