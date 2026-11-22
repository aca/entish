package complete

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"main.go", "make.sh", "my dir/x.txt", ".hidden", "sub/a.go", "Makefile", "Docs/b"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	home := t.TempDir()
	for _, f := range []string{"src/x", "my docs/y"} {
		os.MkdirAll(filepath.Join(home, f), 0o755)
	}
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	c := Files(func() string { return dir }, getenv)
	cases := []struct {
		line string
		want []string
	}{
		{"cat ma", []string{"main.go", "make.sh"}},
		{"cat Ma", []string{"Makefile"}},
		{"cat MA", []string{"Makefile", "main.go", "make.sh"}}, // no exact match: case ignored
		{"cat do", []string{"Docs/"}},
		{"cat docs/", nil}, // only the last part is matched ignoring case
		{"cat mai", []string{"main.go"}},
		{"cat ", []string{"Docs/", "Makefile", "main.go", "make.sh", "'my dir/", "sub/"}},
		{"cat .h", []string{".hidden"}},
		{"cat sub/", []string{"sub/a.go"}},
		{"cat 'my dir/", []string{"'my dir/x.txt'"}},
		{"ls|cat su", []string{"sub/"}},
		{"cat nope/x", nil},
		{"echo $HO", nil},
		{"ls ~/s", []string{"~/src/"}},
		{"ls ~/src/", []string{"~/src/x/"}},
		{"ls ~/m", []string{"~/'my docs/"}},
		{"ls ~/'my docs/", []string{"~/'my docs/y/"}},
	}
	for _, tc := range cases {
		buf := []rune(tc.line)
		comp, err := c(context.Background(), buf, len(buf))
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		var got []string
		for _, it := range comp.Items {
			got = append(got, it.Text)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q: got %q, want %q", tc.line, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: got %q, want %q", tc.line, got, tc.want)
				break
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c(ctx, []rune("cat "), 4); err == nil {
		t.Error("canceled completion returned no error")
	}
}
