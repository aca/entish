package main

import (
	"slices"
	"testing"

	"github.com/aca/entish/lang"
)

func TestDirsBackForward(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	in := lang.New(lang.Options{})
	d := &dirs{k: &keys{in: in, after: func() {}}}
	prompt := func() { d.seen(in.Cwd()) }
	cd := func(dir string) {
		if err := in.Cd(dir); err != nil {
			t.Fatal(err)
		}
		prompt()
	}
	cd(a)
	cd(b)
	cd(c)
	d.Back()
	prompt()
	d.Back()
	prompt()
	if in.Cwd() != a {
		t.Fatalf("after two Back: %s, want %s", in.Cwd(), a)
	}
	d.Forward()
	prompt()
	if in.Cwd() != b {
		t.Fatalf("after Forward: %s, want %s", in.Cwd(), b)
	}
	cd(c) // a new move clears forward
	if len(d.fwd) != 0 || !slices.Equal(d.back, []string{a, b}) {
		t.Fatalf("back %v fwd %v", d.back, d.fwd)
	}
	d.Forward() // nothing to go to
	prompt()
	if in.Cwd() != c {
		t.Fatalf("Forward with an empty stack moved to %s", in.Cwd())
	}
}
