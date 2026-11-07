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
	Desc    string // shown dimmed after Display, one item per row
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

	all        []Item // the completer's items; menu is those matching what was typed since
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
		c.all, c.menu, c.menuIdx, c.menuStart, c.menuEnd = items, items, 0, r.c.Start, r.c.End
		c.menuBuf = string(e.buf)
	}
}

// menuType: typing a char or Backspace in an open menu edits the word as
// usual and narrows the menu to the items that match the word fuzzily,
// instead of closing it. The menu closes when nothing matches.
func (e *editor) menuType(k rune) bool {
	c := &e.comp
	backspace := k == 127 || k == 8
	if !backspace && k <= ' ' || e.mode != insertMode || c.menu == nil ||
		string(e.buf) != c.menuBuf || e.pos != c.menuEnd {
		return false
	}
	if backspace && e.pos <= c.menuStart {
		return false // past the start of the word: the menu closes
	}
	n := len(e.buf)
	e.insertKey(k)
	c.menuEnd += len(e.buf) - n
	q := []rune(strings.ToLower(string(e.buf[c.menuStart:c.menuEnd])))
	var menu []Item
	for _, it := range c.all {
		if subsequence(q, strings.ToLower(it.Text)) {
			menu = append(menu, it)
		}
	}
	if menu == nil {
		e.cancelCompletion()
		return true
	}
	c.menu, c.menuIdx, c.menuBuf = menu, 0, string(e.buf)
	return true
}

// subsequence reports whether the runes of q appear in s in order.
func subsequence(q []rune, s string) bool {
	for _, r := range s {
		if len(q) > 0 && r == q[0] {
			q = q[1:]
		}
	}
	return len(q) == 0
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
	w, desc := 0, false
	for _, it := range c.menu {
		w = max(w, strWidth(it.Display))
		desc = desc || it.Desc != ""
	}
	w += 2
	per := max(1, (cols-1)/w)
	if desc { // one item per row, its description after it
		w, per = min(w, (cols-1)/2), 1
	}
	rows := (len(c.menu) + per - 1) / per
	shown := min(rows, maxMenuRows)
	top := max(0, c.menuIdx/per-shown+1) // scrolled so the selected item shows
	var out []string
	for r := top; r < top+shown; r++ {
		var b strings.Builder
		for col := range per {
			i := r*per + col
			if i >= len(c.menu) {
				break
			}
			it := c.menu[i]
			d := truncate(it.Display, w-2)
			pad := strings.Repeat(" ", w-strWidth(d))
			if i == c.menuIdx {
				b.WriteString("\x1b[7m" + d + "\x1b[0m" + pad)
			} else {
				b.WriteString(d + pad)
			}
			if it.Desc != "" {
				b.WriteString("\x1b[2m" + truncate(it.Desc, cols-1-w) + "\x1b[0m")
			}
		}
		out = append(out, b.String())
	}
	if n := len(c.menu) - (top+shown)*per; n > 0 {
		out = append(out, "\x1b[2m… "+strconv.Itoa(n)+" more\x1b[0m")
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
