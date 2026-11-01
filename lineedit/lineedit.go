// Package lineedit reads input from a user. The Editor interface is what
// the REPL depends on, so the built-in vi-style editor can be replaced by
// another one later (e.g. an embedded neovim).
package lineedit

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

type Editor interface {
	// ReadInput reads one complete input, which may span several lines.
	// more reports whether the text so far is unfinished; then Enter starts
	// a new line instead of accepting. prompt is shown on the first line,
	// cont on the others; rprompt right-aligned on the first line while
	// there is room. It returns ErrInterrupted on Ctrl+C.
	ReadInput(prompt, rprompt, cont string, more func(string) bool) (string, error)
	AddHistory(input string)
	// SetRightPrompt replaces the right prompt of the input being read.
	// It may be called from any goroutine; the editor redraws at once.
	SetRightPrompt(rprompt string)
	// SetPrompt replaces the prompt of the input being read, e.g. after a
	// bound key changed the directory. It may be called from any goroutine.
	SetPrompt(prompt string)
	Close() error
}

var ErrInterrupted = errors.New("interrupted")

// Options are the editor's pluggable parts; any may be nil.
type Options struct {
	Complete Completer   // Tab
	History  History     // ↑ ↓, Ctrl+R; persisted
	Suggest  Suggester   // gray hint after the cursor, taken with Ctrl+F
	Abbr     Abbr        // Space, Enter
	Keys     Keys        // keys bound by the user
	Dirs     Dirs        // Ctrl+D
	Color    Highlighter // the input as it is typed
}

// Highlighter colors the input. It is called on every redraw with the whole
// input, so it must be fast. Spans are rune offsets into buf, sorted and not
// overlapping; Style is an SGR sequence such as "\x1b[34m".
type Highlighter func(buf []rune) []Span

type Span struct {
	Start, End int
	Style      string
}

// Dirs feeds location mode (Ctrl+D): pick a recent directory to cd to.
type Dirs interface {
	Dirs() []Dir // most recent first
	// Cd runs with the terminal, like Keys.Run.
	Cd(dir string)
}

// Dir is a directory with the time it was last visited.
type Dir struct {
	Path string
	Time time.Time
}

// Keys are the user's key bindings, named ctrl-x or alt-c. While Run runs,
// the input is erased and the terminal is handed over, so it may run fzf or
// print; then the input is drawn again below anything it printed, with the
// text Run returns inserted at the cursor, or replacing the whole input if
// replace is true.
type Keys interface {
	Bound(key string) bool
	Run(key, input string) (text string, replace bool)
}

// Abbr expands the word before pos when Space or Enter is typed: it
// returns the text that replaces buf[start:pos], or ok false.
type Abbr func(buf []rune, pos int) (start int, text string, ok bool)

// New returns the vi-style editor when in and out are terminals, and a
// plain line reader otherwise.
func New(in, out *os.File, o Options) Editor {
	if IsTerminal(in) && IsTerminal(out) {
		return newEditor(in, out, o)
	}
	return &plain{r: bufio.NewReader(in)}
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool { return isTerminal(int(f.Fd())) }

type plain struct{ r *bufio.Reader }

func (p *plain) ReadInput(_, _, _ string, more func(string) bool) (string, error) {
	var lines []string
	for {
		line, err := p.r.ReadString('\n')
		if err == io.EOF && line != "" {
			err = nil
		}
		if err != nil {
			if len(lines) > 0 {
				return strings.Join(lines, "\n"), nil
			}
			return "", err
		}
		lines = append(lines, strings.TrimSuffix(line, "\n"))
		text := strings.Join(lines, "\n")
		if more == nil || !more(text) {
			return text, nil
		}
	}
}

func (p *plain) AddHistory(string)     {}
func (p *plain) SetRightPrompt(string) {}
func (p *plain) SetPrompt(string)      {}
func (p *plain) Close() error          { return nil }
