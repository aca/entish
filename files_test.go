package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aca/entish/lang"
)

func TestFileItemsShellStateAndQuoting(t *testing.T) {
	bin, cwd := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$PWD\" = \"$EXPECTED_CWD\" ] || exit 3\n" +
		"[ \"$*\" = '--hidden --type f --strip-cwd-prefix -0' ] || exit 4\n" +
		"printf '%s\\0' 'a b' \"it's\" 'line\nbreak'\n"
	if err := os.WriteFile(filepath.Join(bin, "fd"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	in := lang.New(lang.Options{})
	if err := in.Cd(cwd); err != nil {
		t.Fatal(err)
	}
	if err := in.RunScript("export PATH="+lang.Quote(bin)+" EXPECTED_CWD="+lang.Quote(cwd), "t"); err != nil {
		t.Fatal(err)
	}
	source := fileItems(in)()
	// A canceled request can finish after the user changes shell state.
	// Its source must use the snapshot captured when the picker opened.
	if err := in.Cd(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	items, err := source(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names, texts []string
	for _, item := range items {
		names = append(names, item.Display)
		texts = append(texts, item.Text)
	}
	if !slices.Equal(names, []string{"a b", "it's", "line\nbreak"}) || !slices.Equal(texts, []string{"'a b'", "'it''s'", `"line\nbreak"`}) {
		t.Fatalf("names %q, insertion text %q", names, texts)
	}
}
