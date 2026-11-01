package lineedit

import (
	"context"
	"strings"
	"time"
)

// Suggester proposes a full input that extends buf, e.g. the most recent
// matching history entry; later possibly an AI tool. Like a Completer it
// runs in its own goroutine and the editor never waits for it. It returns
// "" for no suggestion.
type Suggester func(ctx context.Context, buf string) (string, error)

// History stores accepted inputs.
type History interface {
	Add(input string) error
	// Entries returns unique inputs, oldest first. It may include inputs
	// added by other shells since the last call.
	Entries() []string
	// Last returns when and in which directory input was last run.
	Last(input string) (t time.Time, dir string, ok bool)
}

type sugState struct {
	gen     int
	cancel  context.CancelFunc
	pending bool
	reqBuf  string
	text    string // the current suggestion, a full input
}

type sugResult struct {
	gen  int
	text string
}

// updateSuggestion asks for a suggestion when the buffer changed. The old
// one stays visible while it still extends the buffer, so typing along a
// suggestion doesn't flicker.
func (e *editor) updateSuggestion() {
	s := &e.sug
	buf := string(e.buf)
	if e.suggest == nil || buf == s.reqBuf {
		return
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.reqBuf, s.pending = buf, false
	if !strings.HasPrefix(s.text, buf) || s.text == buf {
		s.text = ""
	}
	if strings.TrimSpace(buf) == "" {
		s.text = ""
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.gen++
	s.cancel, s.pending = cancel, true
	gen := s.gen
	go func() {
		text, err := e.suggest(ctx, buf)
		if err != nil {
			text = ""
		}
		e.resMu.Lock()
		e.sugRes = &sugResult{gen: gen, text: text}
		e.resMu.Unlock()
		e.wakeUp()
	}()
}

func (e *editor) wakeSuggestion() {
	s := &e.sug
	if !s.pending {
		return
	}
	e.resMu.Lock()
	r := e.sugRes
	if r != nil && r.gen == s.gen {
		e.sugRes = nil
	}
	e.resMu.Unlock()
	if r == nil || r.gen != s.gen {
		return
	}
	s.pending, s.cancel = false, nil
	if string(e.buf) == s.reqBuf && strings.HasPrefix(r.text, s.reqBuf) && r.text != s.reqBuf {
		s.text = r.text
	} else {
		s.text = ""
	}
}

func (e *editor) clearSuggestion() {
	if e.sug.cancel != nil {
		e.sug.cancel()
	}
	e.sug = sugState{gen: e.sug.gen}
}

// hint is the part of the suggestion after the buffer, shown in gray
// after the cursor at the end of the input.
func (e *editor) hint() string {
	buf := string(e.buf)
	if e.mode != insertMode || e.pos != len(e.buf) || e.search != nil || e.comp.menu != nil ||
		e.sug.text == "" || !strings.HasPrefix(e.sug.text, buf) {
		return ""
	}
	return e.sug.text[len(buf):]
}

// acceptHint takes the suggestion (Ctrl+F, or → at the end).
func (e *editor) acceptHint() bool {
	h := e.hint()
	if h == "" {
		return false
	}
	e.edit(func() { e.insert(h) })
	return true
}
