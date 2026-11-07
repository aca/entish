package complete

import (
	"strings"

	"github.com/aca/entish/lineedit"
)

// Abbr expands a word where a command can start, if lookup knows it.
func Abbr(lookup func(string) (string, bool)) lineedit.Abbr {
	return func(buf []rune, pos int) (int, string, bool) {
		if pos < len(buf) && !isBoundary(buf[pos]) {
			return 0, "", false // pos is inside a word
		}
		start := wordStart(buf, pos)
		if start == pos || !isHead(buf, start) || inText(buf, start) {
			return 0, "", false
		}
		text, ok := lookup(string(buf[start:pos]))
		return start, text, ok
	}
}

// inText reports whether pos is inside a string or a comment. Strings and
// comments end at the end of the line; a line starting with \\ or $\\ is
// all string.
func inText(buf []rune, pos int) bool {
	lineStart := 0
	var quote rune // ' or " while in a string, # in a comment
	for i := 0; i < pos; i++ {
		r := buf[i]
		switch {
		case r == '\n':
			lineStart, quote = i+1, 0
		case quote == '"' && r == '\\':
			i++
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#' && (i == 0 || isBoundary(buf[i-1])):
			quote = '#'
		}
	}
	line := strings.TrimLeft(string(buf[lineStart:pos]), " \t")
	return quote != 0 || strings.HasPrefix(line, `\\`) || strings.HasPrefix(line, `$\\`)
}
