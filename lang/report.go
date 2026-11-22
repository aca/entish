package lang

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// source is the text a Pos points into.
type source struct {
	text   []rune
	starts []int // offset of each line
}

func (s *source) line(n int) (string, bool) {
	if s == nil || n < 1 || n > len(s.starts) {
		return "", false
	}
	end := len(s.text)
	if n < len(s.starts) {
		end = s.starts[n] - 1
	}
	return string(s.text[s.starts[n-1]:end]), true
}

// mark is a failed stage of a pipeline.
type mark struct {
	at    Pos
	label string
}

// frame is a fn call a raise came out of.
type frame struct {
	what string
	at   Pos // the call; zero if a builtin called the fn
}

// addFrame records on a raise that it came out of f.
func addFrame(err error, f frame) {
	var r *Raise
	if errors.As(err, &r) && !r.intr {
		r.trace = append(r.trace, f)
	}
}

const maxFrames = 10

// Report is the error as shown to the user: the message, the source line
// with the failed stages marked, and the fn calls it came out of. Error()
// stays one line, like the error map.
func (r *Raise) Report() string {
	if r.intr || r.at.src == nil {
		return r.Error()
	}
	m, _ := r.Val.(*Map)
	msg := r.Error()
	if m != nil {
		msg, _ = m.M["msg"].(string)
	}
	marks := r.marks
	if marks == nil {
		marks = []mark{{at: r.at}}
	}
	width := len(fmt.Sprint(r.at.Line))
	for _, k := range marks {
		width = max(width, len(fmt.Sprint(k.at.Line)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%*s--> %s\n", msg, width, "", r.at)
	gutter := strings.Repeat(" ", width) + " |"
	b.WriteString(gutter + "\n")
	for i := 0; i < len(marks); {
		at := marks[i].at
		text, ok := at.src.line(at.Line)
		if !ok {
			i++
			continue
		}
		fmt.Fprintf(&b, "%*d | %s\n", width, at.Line, text)
		for ; i < len(marks) && marks[i].at.Line == at.Line && marks[i].at.src == at.src; i++ {
			fmt.Fprintf(&b, "%s %s\n", gutter, strings.TrimRight(underline(text, marks[i].at.Col)+" "+marks[i].label, " "))
		}
	}
	shown := 0
	for i := 0; i < len(r.trace); {
		if shown == maxFrames {
			fmt.Fprintf(&b, "  ... %d more\n", len(r.trace)-i)
			break
		}
		f := r.trace[i]
		n := 1 // a recursive fn repeats the same frame
		for i+n < len(r.trace) && r.trace[i+n] == f {
			n++
		}
		b.WriteString("  " + f.what)
		if f.at != (Pos{}) {
			b.WriteString(", called at " + f.at.String())
			if text, ok := f.at.src.line(f.at.Line); ok {
				b.WriteString(": " + short(string([]rune(text)[f.at.Col-1:])))
			}
		}
		if n > 1 {
			fmt.Fprintf(&b, " (%d times)", n)
		}
		b.WriteString("\n")
		i += n
		shown++
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// underline marks the word of text at col with ^, keeping its tabs so the
// marks line up.
func underline(text string, col int) string {
	rs := []rune(text)
	var b strings.Builder
	for i := 0; i < col-1 && i < len(rs); i++ {
		if rs[i] == '\t' {
			b.WriteRune('\t')
		} else {
			b.WriteRune(' ')
		}
	}
	n := 0
	for i := col - 1; i < len(rs) && !unicode.IsSpace(rs[i]); i++ {
		n++
	}
	return b.String() + strings.Repeat("^", max(n, 1))
}

// short cuts s to one line of at most 60 chars.
func short(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " ..."
	}
	if rs := []rune(s); len(rs) > 60 {
		s = string(rs[:57]) + "..."
	}
	return s
}
