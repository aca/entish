package complete

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/aca/entish/lineedit"
)

// Shell completes command names where a command can start (keywords, fns,
// builtins, executables on PATH), arguments of externals with args, and
// paths everywhere else (also when args is nil).
//
// PATH is scanned in the background when Shell is created and again when
// PATH changes; until a scan finishes, completion offers what is known so
// far (possibly nothing). Exactness is traded for never waiting.
func Shell(cwd func() string, getenv func(string) string, names func() []string, args Args) lineedit.Completer {
	files := Files(cwd, getenv)
	ex := &executables{}
	ex.list(getenv("PATH")) // start the first scan now
	return func(ctx context.Context, buf []rune, pos int) (lineedit.Completion, error) {
		start := wordStart(buf, pos)
		word := string(buf[start:pos])
		if strings.ContainsAny(word, "/'$") || !isHead(buf, start) {
			if args != nil && !strings.ContainsAny(word, "$/") { // a path: files, which expand ~/
				if words, ok := commandWords(buf, start, pos); ok && len(words) > 1 {
					if w, caret := strings.CutPrefix(words[0], "^"); caret || !slices.Contains(names(), w) {
						words[0] = w
						return argItems(ctx, args, words, start, pos, strings.HasPrefix(word, "'"))
					}
				}
			}
			return files(ctx, buf, pos)
		}
		caret := strings.HasPrefix(word, "^") // ^name: externals only
		prefix := strings.TrimPrefix(word, "^")
		seen := map[string]bool{}
		var items, folded []lineedit.Item // folded: matches only when case is ignored
		add := func(n string) {
			exact := strings.HasPrefix(n, prefix)
			if !exact && !hasPrefixFold(n, prefix) || seen[n] {
				return
			}
			seen[n] = true
			text := n
			if caret {
				text = "^" + n
			}
			if exact {
				items = append(items, lineedit.Item{Text: text, Display: n})
			} else {
				folded = append(folded, lineedit.Item{Text: text, Display: n})
			}
		}
		if !caret {
			for _, n := range names() {
				add(n)
			}
		}
		for _, n := range ex.list(getenv("PATH")) {
			add(n)
		}
		if len(items) == 0 {
			items = folded
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Display < items[j].Display })
		return lineedit.Completion{Start: start, End: pos, Items: items}, nil
	}
}

func argItems(ctx context.Context, args Args, words []string, start, pos int, quoted bool) (lineedit.Completion, error) {
	items, err := args(ctx, words)
	if err != nil {
		return lineedit.Completion{}, err
	}
	for i := range items {
		items[i].Text = quote(items[i].Text, quoted)
	}
	return lineedit.Completion{Start: start, End: pos, Items: items}, nil
}

// commandWords returns the words of the command that has an argument
// starting at start, unquoted, ending with the partial buf[start:pos]. ok
// is false when something other than words precedes it (a redirect, …).
func commandWords(buf []rune, start, pos int) (words []string, ok bool) {
	words = []string{unquote(string(buf[start:pos]))}
	for s := start; !isHead(buf, s); {
		end := s
		for end > 0 && (buf[end-1] == ' ' || buf[end-1] == '\t') {
			end--
		}
		if isBoundary(buf[end-1]) {
			return nil, false
		}
		s = wordStart(buf, end)
		words = append([]string{unquote(string(buf[s:end]))}, words...)
	}
	return words, true
}

// unquote returns the value of a '...' word, which may be unterminated.
// Other words are returned as they are.
func unquote(w string) string {
	if !strings.HasPrefix(w, "'") {
		return w
	}
	w = w[1:]
	if strings.HasSuffix(w, "'") && strings.Count(w, "'")%2 == 1 {
		w = w[:len(w)-1]
	}
	return strings.ReplaceAll(w, "''", "'")
}

// executables caches the names of executables on PATH.
type executables struct {
	mu      sync.Mutex
	path    string // PATH of the cached or running scan
	names   []string
	scanned bool
}

// list returns the cached names and starts a scan if PATH changed.
func (x *executables) list(path string) []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.scanned || path != x.path {
		x.path, x.scanned = path, true
		go x.scan(path)
	}
	return x.names
}

func (x *executables) scan(path string) {
	seen := map[string]bool{}
	var names []string
	for _, dir := range filepath.SplitList(path) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range ents {
			n := de.Name()
			if seen[n] || de.IsDir() {
				continue
			}
			// Symlinks (e.g. nix profiles) are taken as executables
			// without following them; close enough for completion.
			if de.Type().IsRegular() {
				if fi, err := de.Info(); err != nil || fi.Mode()&0o111 == 0 {
					continue
				}
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	x.mu.Lock()
	if x.path == path { // a newer scan may have started meanwhile
		x.names = names
	}
	x.mu.Unlock()
}

// isHead reports whether a word starting at start is in command position:
// the start of a statement, after | |> && || ; { ( $( ! =, after `if`, or
// after a lambda's |params|.
func isHead(buf []rune, start int) bool {
	i := start - 1
	for i >= 0 && (buf[i] == ' ' || buf[i] == '\t') {
		i--
	}
	if i < 0 {
		return true
	}
	switch buf[i] {
	case '\n', ';', '|', '&', '{', '(', '!', '=':
		return true
	case '>':
		return i > 0 && buf[i-1] == '|' // |>
	}
	end := i + 1
	for i >= 0 && !isBoundary(buf[i]) {
		i--
	}
	return string(buf[i+1:end]) == "if"
}
