package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aca/entish/lang"
	"github.com/aca/entish/lineedit"
)

// Keep the same hidden-file and ignore rules as the former Ctrl+T binding.
// Only candidate collection runs here; the editor owns the UI and input.
func fileItems(in *lang.Interp) func() lineedit.FileSource {
	return func() lineedit.FileSource {
		cwd, env := in.Cwd(), in.Environ()
		path, pathErr := in.LookPath("fd")
		return func(ctx context.Context) ([]lineedit.Item, error) {
			if pathErr != nil {
				return nil, pathErr
			}
			cmd := exec.CommandContext(ctx, path, "--hidden", "--type", "f", "--strip-cwd-prefix", "-0")
			cmd.Dir, cmd.Env = cwd, env
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				return nil, fmt.Errorf("files: %w: %s", err, strings.TrimSpace(stderr.String()))
			}
			var items []lineedit.Item
			for _, name := range bytes.Split(out, []byte{0}) {
				if len(name) > 0 {
					s := string(name)
					items = append(items, lineedit.Item{Text: lang.Quote(s), Display: s})
				}
			}
			return items, nil
		}
	}
}
