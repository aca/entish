package lineedit

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Completer computes completions for the buffer with the cursor at pos. It
// runs in its own goroutine and must return soon after ctx is canceled;
// the editor never waits for it.
type Completer func(ctx context.Context, buf []rune, pos int) (Completion, error)

// Completion replaces buf[Start:End] with one of Items.
type Completion struct {
	Start, End int
	Items      []Item
}

type Item struct {
	Text    string // inserted into the buffer
	Display string // shown in the menu
	NoSpace bool   // no space after it (e.g. a directory)
}

// pendingNotice is how long a completion may run before "completing…" is
// shown; faster ones never flicker.
const pendingNotice = 150 * time.Millisecond

const maxMenuRows = 8

type compResult struct {
	gen int
	c   Completion
	err error
}

type compState struct {
	gen     int
	cancel  context.CancelFunc
	pending bool
	reqBuf  string
	reqPos  int
	started time.Time

	menu       []Item
	menuIdx    int
	menuStart  int
	menuEnd    int
	menuBuf    string // buffer when the menu was last shown or cycled
	statusLine string
}

// tab starts a completion, or cycles through an open menu.
func (e *editor) tab() {
	c := &e.comp
	if c.menu != nil && string(e.buf) == c.menuBuf {
		c.menuIdx = (c.menuIdx + 1) % len(c.menu) // the buffer is left alone until Enter
		return
	}
	if e.complete == nil {
		return
	}
	e.cancelCompletion()
	ctx, cancel := context.WithCancel(context.Background())
	c.gen++
	c.cancel, c.pending = cancel, true
	c.reqBuf, c.reqPos, c.started = string(e.buf), e.pos, time.Now()
	gen, buf, pos := c.gen, append([]rune(nil), e.buf...), e.pos
	go func() {
		comp, err := e.complete(ctx, buf, pos)
		e.resMu.Lock()
		if e.result == nil || e.result.gen < gen { // a canceled request may finish after a newer one
			e.result = &compResult{gen: gen, c: comp, err: err}
		}
		e.resMu.Unlock()
		e.wakeUp()
	}()
	time.AfterFunc(pendingNotice, e.wakeUp)
}

func (e *editor) wakeUp() {
	if e.wakeW != nil {
		e.wakeW.Write([]byte{1})
	}
}

// cancelCompletion drops a running completion and closes the menu. Any key
// other than Tab does this, so typing is never held up by a slow completer.
func (e *editor) cancelCompletion() {
	c := &e.comp
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.pending, c.menu, c.statusLine = false, nil, ""
}

// wake handles finished async requests.
func (e *editor) wake() {
	e.wakeCompletion()
	e.wakeSuggestion()
}

// wakeCompletion handles a completion result, or shows that one is still
// running.
func (e *editor) wakeCompletion() {
	c := &e.comp
	if !c.pending {
		return
	}
	e.resMu.Lock()
	r := e.result
	e.result = nil
	e.resMu.Unlock()
	if r == nil || r.gen != c.gen {
		// woken by the notice timer or by another request (a suggestion)
		if time.Since(c.started) >= pendingNotice {
			c.statusLine = "completing…"
		}
		return
	}
	c.pending, c.cancel, c.statusLine = false, nil, ""
	if string(e.buf) != c.reqBuf || e.pos != c.reqPos {
		return // the buffer changed meanwhile
	}
	items := r.c.Items
	switch {
	case r.err != nil:
		c.statusLine = "completion failed: " + r.err.Error()
	case len(items) == 0:
		c.statusLine = "no matches"
	case len(items) == 1:
		text := items[0].Text
		if !items[0].NoSpace {
			text += " "
		}
		e.edit(func() { e.replace(r.c.Start, r.c.End, text) })
	default: // select the first item; Tab moves on, Enter takes it
		c.menu, c.menuIdx, c.menuStart, c.menuEnd = items, 0, r.c.Start, r.c.End
		c.menuBuf = string(e.buf)
	}
}

// menuEnter: Enter on an open menu puts the selected item into the buffer
// and closes the menu instead of running the input.
func (e *editor) menuEnter(k rune) bool {
	c := &e.comp
	if e.mode != insertMode || k != '\r' && k != '\n' || c.menu == nil || string(e.buf) != c.menuBuf {
		return false
	}
	it := c.menu[c.menuIdx]
	text := it.Text
	if !it.NoSpace {
		text += " "
	}
	e.edit(func() { e.replace(c.menuStart, c.menuEnd, text) })
	e.cancelCompletion()
	return true
}

func (e *editor) replace(a, b int, s string) {
	e.deleteRange(a, b)
	e.insert(s)
}

// belowLines is what refresh draws under the input: the menu or a status.
func (e *editor) belowLines(cols int) []string {
	c := &e.comp
	if c.menu == nil {
		if c.statusLine != "" {
			return []string{"\x1b[2m" + truncate(c.statusLine, cols-1) + "\x1b[0m"}
		}
		return nil
	}
	w := 0
	for _, it := range c.menu {
		w = max(w, strWidth(it.Display))
	}
	w += 2
	per := max(1, (cols-1)/w)
	rows := (len(c.menu) + per - 1) / per
	shown := min(rows, maxMenuRows)
	var out []string
	for r := range shown {
		var b strings.Builder
		for col := range per {
			i := r*per + col
			if i >= len(c.menu) {
				break
			}
			d := truncate(c.menu[i].Display, w-2)
			pad := strings.Repeat(" ", w-strWidth(d))
			if i == c.menuIdx {
				b.WriteString("\x1b[7m" + d + "\x1b[0m" + pad)
			} else {
				b.WriteString(d + pad)
			}
		}
		out = append(out, b.String())
	}
	if shown < rows {
		out = append(out, "\x1b[2m… "+strconv.Itoa(len(c.menu)-shown*per)+" more\x1b[0m")
	}
	return out
}

func truncate(s string, w int) string {
	rs := []rune(s)
	for len(rs) > 0 && runesWidth(rs) > w {
		rs = rs[:len(rs)-1]
	}
	return string(rs)
}
