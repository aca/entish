package complete

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestFish(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("no fish")
	}
	dir := t.TempDir()
	for _, f := range []string{"main.go", "make.sh", "my 'dir/x.txt"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // not the user's config.fish
	f := Fish(func() string { return dir }, os.Environ)
	cases := []struct {
		words []string
		want  []string
	}{
		{[]string{"cat", "ma"}, []string{"main.go", "make.sh"}},
		{[]string{"cat", "my 'd"}, []string{"my 'dir/"}},
		{[]string{"cat", "my 'dir/"}, []string{"my 'dir/x.txt"}},
	}
	for _, tc := range cases {
		items, err := f(context.Background(), tc.words)
		if err != nil {
			t.Fatalf("%q: %v", tc.words, err)
		}
		var got []string
		for _, it := range items {
			got = append(got, it.Text)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.words, got, tc.want)
		}
	}
}
