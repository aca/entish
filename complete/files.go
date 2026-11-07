// Package complete holds completers for the line editor.
package complete

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aca/entish/lineedit"
)

// Files completes the word under the cursor as a path, relative to the
// shell's cwd (not the process cwd). An unquoted ~/ is the shell's $HOME.
func Files(cwd func() string, getenv func(string) string) lineedit.Completer {
	return func(ctx context.Context, buf []rune, pos int) (lineedit.Completion, error) {
		start := wordStart(buf, pos)
		word := string(buf[start:pos])
		base := cwd()
		prefix := "" // ~/ stays in front, outside any quote: a quoted ~ does not expand
		if rest, ok := strings.CutPrefix(word, "~/"); ok {
			base, prefix, word = getenv("HOME"), "~/", rest
		}
		quoted := strings.HasPrefix(word, "'")
		raw := word
		if quoted {
			raw = strings.ReplaceAll(strings.TrimSuffix(word[1:], "'"), "''", "'")
		}
		if strings.ContainsAny(raw, "$*?[") && !quoted {
			return lineedit.Completion{}, nil // variables and globs are not paths yet
		}
		dir, file := filepath.Split(raw)
		names, err := list(ctx, resolve(base, dir), file)
		if err != nil {
			return lineedit.Completion{}, err
		}
		c := lineedit.Completion{Start: start, End: pos}
		for _, n := range names {
			text := dir + n.name
			display := n.name
			if n.dir {
				text += "/"
				display += "/"
			}
			c.Items = append(c.Items, lineedit.Item{Text: prefix + quote(text, quoted), Display: display, NoSpace: n.dir})
		}
		return c, nil
	}
}

func resolve(cwd, dir string) string {
	switch {
	case dir == "":
		return cwd
	case filepath.IsAbs(dir):
		return dir
	}
	return filepath.Join(cwd, dir)
}

type entry struct {
	name string
	dir  bool
}

// list reads dir in chunks and stops as soon as ctx is canceled, so a slow
// directory (network mount, huge dir) only costs the time it is waited on.
func list(ctx context.Context, dir, prefix string) ([]entry, error) {
	f, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	hidden := strings.HasPrefix(prefix, ".")
	var out, folded []entry // folded: matches only when case is ignored
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ents, err := f.ReadDir(256)
		for _, de := range ents {
			name := de.Name()
			exact := strings.HasPrefix(name, prefix)
			if !exact && !hasPrefixFold(name, prefix) || !hidden && strings.HasPrefix(name, ".") {
				continue
			}
			isDir := de.IsDir()
			if de.Type()&os.ModeSymlink != 0 {
				if fi, err := os.Stat(filepath.Join(dir, name)); err == nil {
					isDir = fi.IsDir()
				}
			}
			if exact {
				out = append(out, entry{name, isDir})
			} else {
				folded = append(folded, entry{name, isDir})
			}
		}
		if err == io.EOF || len(ents) == 0 {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		out = folded
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// hasPrefixFold is strings.HasPrefix ignoring case. Completers offer such
// matches only when nothing matches with the case as typed.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// wordStart finds the start of the word ending at pos: after a blank or a
// char that ends an entish word.
func wordStart(buf []rune, pos int) int {
	inQuote := false
	start := pos
	for i := 0; i < pos; i++ {
		switch r := buf[i]; {
		case r == '\'':
			if !inQuote && (i == 0 || isBoundary(buf[i-1])) {
				start = i
			}
			inQuote = !inQuote
		case inQuote:
		case isBoundary(r):
			start = i + 1
		case i == 0 || isBoundary(buf[i-1]):
			start = i
		}
	}
	if start > pos {
		start = pos
	}
	return start
}

func isBoundary(r rune) bool { return strings.ContainsRune(" \t\n|;&<>(){}[]", r) }

// quote returns s as an entish word, using '...' when it has special chars.
func quote(s string, force bool) string {
	if !force && !strings.ContainsAny(s, " \t\n|;&<>(){}[]'\"$*?#\\`") {
		return s
	}
	q := "'" + strings.ReplaceAll(s, "'", "''")
	if strings.HasSuffix(s, "/") {
		return q // keep the quote open so completion can continue inside it
	}
	return q + "'"
}
