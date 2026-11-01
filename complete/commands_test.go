package complete

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aca/entish/lineedit"
)

func TestIsHead(t *testing.T) {
	cases := map[string]bool{
		"gi":                   true,
		"ls | gr":              true,
		"ls |> li":             true,
		"make && ma":           true,
		"a; b":                 true,
		"if te":                true,
		"let x = ec":           true,
		"[1] |> each { |x| ec": true,
		"[1] |> each {|x| ec":  true,
		"ls fi":                false,
		"git co":               false,
		"echo a > ou":          false,
		"if test -f fi":        false,
	}
	for line, want := range cases {
		buf := []rune(line)
		if got := isHead(buf, wordStart(buf, len(buf))); got != want {
			t.Errorf("%q: isHead = %v, want %v", line, got, want)
		}
	}
}

func TestShell(t *testing.T) {
	bin := t.TempDir()
	for _, n := range []string{"gitk", "grep", "notexec"} {
		mode := os.FileMode(0o755)
		if n == "notexec" {
			mode = 0o644
		}
		os.WriteFile(filepath.Join(bin, n), nil, mode)
	}
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "gofile"), nil, 0o644)
	env := map[string]string{"PATH": bin}
	c := Shell(func() string { return cwd }, func(k string) string { return env[k] },
		func() []string { return []string{"if", "git_wrap", "lines"} }, nil)

	texts := func(line string) []string {
		buf := []rune(line)
		comp, err := c(context.Background(), buf, len(buf))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range comp.Items {
			out = append(out, it.Text)
		}
		return out
	}
	// The PATH scan is asynchronous: wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for len(texts("gi")) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	check := func(line string, want ...string) {
		got := texts(line)
		if len(got) != len(want) {
			t.Errorf("%q: got %q, want %q", line, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: got %q, want %q", line, got, want)
				return
			}
		}
	}
	check("gi", "git_wrap", "gitk")
	check("ls | g", "git_wrap", "gitk", "grep")
	check("^g", "^gitk", "^grep")
	check("li", "lines")
	check("no")               // not executable, not offered
	check("cat go", "gofile") // arg position: files
}

func TestShellArgs(t *testing.T) {
	var got []string
	args := func(ctx context.Context, words []string) ([]lineedit.Item, error) {
		got = words
		return []lineedit.Item{{Text: "my dir/", NoSpace: true}}, nil
	}
	cwd := t.TempDir()
	c := Shell(func() string { return cwd }, func(string) string { return "" },
		func() []string { return []string{"lines", "cd"} }, args)
	cases := []struct {
		line  string
		words []string // nil: args not called
		text  string
	}{
		{"git co", []string{"git", "co"}, "'my dir/"},
		{"ls | git -C 'a b' ", []string{"git", "-C", "a b", ""}, "'my dir/"},
		{"cat 'my d", []string{"cat", "my d"}, "'my dir/"},
		{"^lines x", []string{"lines", "x"}, "'my dir/"},
		{"[1] |> each {|x| rg fo", []string{"rg", "fo"}, "'my dir/"},
		{"lines x", nil, ""},     // builtin: files
		{"cd x", nil, ""},        // builtin: files
		{"echo a > ou", nil, ""}, // redirect: files
		{"echo $HO", nil, ""},
		{"mv ~/Downl", nil, ""},   // path: files
		{"cat 'my dir/", nil, ""}, // path: files
		{"./scr", nil, ""},        // command position
	}
	for _, tc := range cases {
		got = nil
		buf := []rune(tc.line)
		comp, err := c(context.Background(), buf, len(buf))
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		if !slices.Equal(got, tc.words) {
			t.Errorf("%q: words %q, want %q", tc.line, got, tc.words)
		}
		if tc.words != nil && (len(comp.Items) != 1 || comp.Items[0].Text != tc.text) {
			t.Errorf("%q: items %v, want %q", tc.line, comp.Items, tc.text)
		}
	}
}
