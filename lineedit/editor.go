package lineedit

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// editor is a multi-line, vi-style editor. Input starts in insert mode,
// which also accepts common emacs keys; Esc switches to normal mode
// (vi.go). The whole input is one buffer; Enter accepts it unless it is
// unfinished.
type editor struct {
	in, out *os.File
	restore func()
	history []string
	last    string // the last input accepted in this session
	readErr error
	pend    []byte // bytes read ahead while parsing escape sequences
	paste   string // text of the last bracketed paste (keyPaste)

	// async completion: a finished completer writes its result and a byte
	// to wakeW; the key loop waits on both the terminal and wakeR.
	complete     Completer
	abbr         Abbr
	keys         Keys
	dirs         Dirs
	color        Highlighter
	pair         Pair
	comp         compState
	resMu        sync.Mutex
	result       *compResult
	wakeR, wakeW *os.File

	newPrompt  *string // set by SetPrompt; under resMu
	resized    bool    // the terminal was resized; under resMu
	newRprompt *string // set by SetRightPrompt from another goroutine; under resMu

	hstore  History
	suggest Suggester
	sug     sugState
	sugRes  *sugResult
	search  *searchState

	// per input
	prompt, rprompt, cont string
	more                  func(string) bool
	buf                   []rune
	pos                   int
	mode                  mode
	cursorRow             int // row of the cursor relative to the first drawn row
	shape                 string
	hist                  []string // history plus the input being edited, last
	hidx                  int
	undo, redo            []snapshot
	insSnap               bool // an undo snapshot covers the current insert session

	// vi state kept across inputs
	reg        []rune
	regLines   bool
	replay     []rune
	recording  bool
	rec        []rune
	lastChange []rune
	lastFind   [2]rune // command (f F t T) and char
}

type mode int

const (
	insertMode mode = iota
	normalMode
)

type snapshot struct {
	buf []rune
	pos int
}

type action int

const (
	actNone action = iota
	actAccept
	actInterrupt
)

// Special keys are negative so they never collide with typed runes.
const (
	keyErr rune = -1 - iota
	keyUp
	keyDown
	keyRight
	keyLeft
	keyHome
	keyEnd
	keyDel
	keyPaste
	keyWake // a completion finished or is slow (not a typed key)
	keyUnknown
	keyAlt rune = -1 << 16 // keyAlt + c is Alt+c
)

const escTimeout = 30 * time.Millisecond

func newEditor(in, out *os.File, o Options) *editor {
	e := &editor{in: in, out: out, complete: o.Complete, abbr: o.Abbr, keys: o.Keys, dirs: o.Dirs, color: o.Color, pair: o.Pair, hstore: o.History, suggest: o.Suggest}
	if r, w, err := os.Pipe(); err == nil {
		e.wakeR, e.wakeW = r, w
	}
	return e
}

func (e *editor) Close() error { return nil }

// AddHistory records an accepted input; a stored history keeps it across
// sessions and shells.
func (e *editor) AddHistory(s string) {
	if s == "" {
		return
	}
	if e.hstore != nil {
		e.hstore.Add(s) // a failed write only loses this entry
	}
	for i, h := range e.history {
		if h == s {
			e.history = append(e.history[:i], e.history[i+1:]...)
			break
		}
	}
	e.history = append(e.history, s)
	e.last = s
}

func (e *editor) ReadInput(prompt, rprompt, cont string, more func(string) bool) (string, error) {
	restore, err := makeRaw(int(e.in.Fd()))
	if err != nil {
		return "", err
	}
	e.restore = restore
	defer func() { e.restore() }()
	e.write("\x1b[?2004h") // bracketed paste: a pasted newline is text, not Enter
	defer onResize(func() {
		e.resMu.Lock()
		e.resized = true
		e.resMu.Unlock()
		e.wakeUp()
	})()
	defer e.write("\x1b[?2004l\x1b[0 q")

	e.prompt, e.rprompt, e.cont, e.more = prompt, rprompt, cont, more

	// Output that did not end in a newline would be drawn over. Like zsh,
	// mark it with % and start a new line: the spaces wrap only when the
	// cursor is not at column 0, and the line left is then cleared.
	e.write("\x1b[7m%\x1b[0m" + strings.Repeat(" ", termCols(int(e.out.Fd()))-1) + "\r\x1b[K")

	e.buf, e.pos, e.mode, e.cursorRow, e.shape = nil, 0, insertMode, 0, ""
	if e.hstore != nil {
		e.history = e.hstore.Entries()
	}
	e.search = nil
	e.clearSuggestion()
	e.hist = append(append([]string(nil), e.history...), "")
	e.hidx = len(e.hist) - 1
	e.undo, e.redo, e.insSnap = nil, nil, false
	e.readErr = nil
	e.cancelCompletion()
	e.refresh()
	for {
		k := e.next()
		if e.readErr != nil {
			return "", e.readErr
		}
		if k == keyWake {
			e.wake()
			e.takePrompts()
			e.refresh()
			continue
		}
		if e.menuEnter(k) {
			e.refresh()
			continue
		}
		if e.search == nil && !e.bound(k) && e.menuType(k) {
			e.updateSuggestion()
			e.refresh()
			continue
		}
		if e.mode != insertMode || k != '\t' {
			e.cancelCompletion()
		}
		var act action
		switch {
		case e.search != nil:
			e.searchKey(k)
		case e.bound(k):
			e.runKey(keyName(k))
		case e.mode == insertMode:
			act = e.insertKey(k)
		default:
			act = e.normalKey(k)
		}
		e.updateSuggestion()
		if e.readErr != nil {
			return "", e.readErr
		}
		switch act {
		case actAccept:
			e.finish("")
			return string(e.buf), nil
		case actInterrupt:
			e.finish("^C")
			return "", ErrInterrupted
		}
		e.refresh()
	}
}

func (e *editor) SetRightPrompt(s string) {
	e.resMu.Lock()
	e.newRprompt = &s
	e.resMu.Unlock()
	e.wakeUp()
}

func (e *editor) SetPrompt(s string) {
	e.resMu.Lock()
	e.newPrompt = &s
	e.resMu.Unlock()
	e.wakeUp()
}

func (e *editor) promptPending() bool {
	e.resMu.Lock()
	defer e.resMu.Unlock()
	return e.newPrompt != nil || e.newRprompt != nil || e.resized
}

// takePrompts applies prompts set by SetPrompt and SetRightPrompt; a
// resize only needs the redraw that follows.
func (e *editor) takePrompts() {
	e.resMu.Lock()
	e.resized = false
	if e.newPrompt != nil {
		e.prompt, e.newPrompt = *e.newPrompt, nil
	}
	if e.newRprompt != nil {
		e.rprompt, e.newRprompt = *e.newRprompt, nil
	}
	e.resMu.Unlock()
}

// finish moves the cursor below the input so output starts on a new line.
// The right prompt is dropped: in the scrollback it would end up in the
// wrong place once the terminal is resized.
func (e *editor) finish(mark string) {
	e.cancelCompletion()
	e.clearSuggestion()
	e.search = nil
	e.rprompt = ""
	e.pos, e.mode = len(e.buf), insertMode
	e.refresh()
	e.write(mark + "\r\n")
	e.cursorRow = 0
}

// ---- keys

func (e *editor) write(s string) { e.out.WriteString(s) }

func (e *editor) readByte() (byte, bool) {
	if len(e.pend) > 0 {
		b := e.pend[0]
		e.pend = e.pend[1:]
		return b, true
	}
	var b [1]byte
	for {
		n, err := e.in.Read(b[:])
		if err != nil {
			e.readErr = err
			return 0, false
		}
		if n == 1 {
			return b[0], true
		}
	}
}

// next returns the next key: a replayed key (vi '.'), or one from the
// terminal. Keys are recorded while a vi change is being recorded.
func (e *editor) next() rune {
	for {
		var k rune
		if len(e.replay) > 0 {
			k, e.replay = e.replay[0], e.replay[1:]
		} else {
			k = e.readKey()
		}
		if k == keyWake && !e.comp.pending && !e.sug.pending && !e.promptPending() {
			continue // a canceled request finished late
		}
		if e.recording && k != keyErr && k != keyWake {
			e.rec = append(e.rec, k)
		}
		return k
	}
}

func (e *editor) readKey() rune {
	if len(e.pend) == 0 && e.wakeR != nil {
		ready := waitAny([]int{int(e.in.Fd()), int(e.wakeR.Fd())}, -1)
		if ready[1] && !ready[0] {
			var drain [64]byte
			e.wakeR.Read(drain[:])
			return keyWake
		}
	}
	b, ok := e.readByte()
	if !ok {
		return keyErr
	}
	if b == 27 {
		if len(e.pend) == 0 && !waitInput(int(e.in.Fd()), escTimeout) {
			return 27
		}
		return e.escape()
	}
	if b < utf8.RuneSelf {
		return rune(b)
	}
	buf := []byte{b}
	for !utf8.FullRune(buf) && len(buf) < utf8.UTFMax {
		b, ok := e.readByte()
		if !ok {
			return keyErr
		}
		buf = append(buf, b)
	}
	r, _ := utf8.DecodeRune(buf)
	return r
}

// escape parses the rest of an escape sequence; anything unknown after a
// lone Esc is treated as Esc followed by that key.
func (e *editor) escape() rune {
	b, ok := e.readByte()
	if !ok {
		return keyErr
	}
	if b != '[' && b != 'O' {
		if e.bound(keyAlt + rune(b)) {
			return keyAlt + rune(b)
		}
		e.pend = append([]byte{b}, e.pend...)
		return 27
	}
	var seq []byte
	for {
		c, ok := e.readByte()
		if !ok {
			return keyErr
		}
		seq = append(seq, c)
		if c >= 0x40 && c <= 0x7e {
			break
		}
	}
	switch string(seq) {
	case "A":
		return keyUp
	case "B":
		return keyDown
	case "C":
		return keyRight
	case "D":
		return keyLeft
	case "H", "1~", "7~":
		return keyHome
	case "F", "4~", "8~":
		return keyEnd
	case "3~":
		return keyDel
	case "200~":
		return e.readPaste()
	}
	return keyUnknown
}

func (e *editor) readPaste() rune {
	const end = "\x1b[201~"
	var b []byte
	for !strings.HasSuffix(string(b), end) {
		c, ok := e.readByte()
		if !ok {
			return keyErr
		}
		b = append(b, c)
	}
	s := strings.TrimSuffix(string(b), end)
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	e.paste = s
	return keyPaste
}

// ---- bound keys

// keyName is the name a key is bound by: ctrl-a .. ctrl-z (but not Tab and
// Enter) and alt-c, or "".
func keyName(k rune) string {
	switch {
	case k >= 1 && k <= 26 && k != '\t' && k != '\n' && k != '\r':
		return "ctrl-" + string('a'+k-1)
	case k > keyAlt+' ' && k < keyAlt+utf8.RuneSelf:
		return "alt-" + string(k-keyAlt)
	}
	return ""
}

func (e *editor) bound(k rune) bool {
	n := keyName(k)
	return n != "" && e.keys != nil && e.keys.Bound(n)
}

// runKey runs a bound key with the terminal and inserts its result, or
// replaces the input with it.
func (e *editor) runKey(name string) {
	var s string
	var replace bool
	e.handOver(func() { s, replace = e.keys.Run(name, string(e.buf)) })
	a, b := e.pos, e.pos
	if replace {
		a, b = 0, len(e.buf)
	} else if s == "" {
		return
	}
	if e.mode == insertMode {
		e.edit(func() { e.replace(a, b, s) })
		return
	}
	e.change()
	e.replace(a, b, s)
	if s != "" {
		e.pos-- // on the last char inserted, as after vi P
	}
}

// handOver runs f with the terminal: the input is erased, and drawn again
// afterwards wherever f left the cursor.
func (e *editor) handOver(f func()) {
	if e.cursorRow > 0 {
		fmt.Fprintf(e.out, "\x1b[%dA", e.cursorRow)
	}
	e.write("\r\x1b[J\x1b[?2004l\x1b[0 q")
	e.cursorRow, e.shape = 0, ""
	e.restore()
	f()
	if restore, err := makeRaw(int(e.in.Fd())); err == nil {
		e.restore = restore
	}
	e.write("\x1b[?2004h")
	e.takePrompts()
}

// ---- insert mode

func (e *editor) insertKey(k rune) action {
	switch k {
	case 27: // Esc
		e.mode = normalMode
		if e.pos > e.lineStart(e.pos) {
			e.pos--
		}
		if e.recording {
			e.lastChange, e.recording = e.rec, false
		}
		e.insSnap = false
	case '\r', '\n':
		return e.enter()
	case 3: // Ctrl+C
		return actInterrupt
	case 4: // Ctrl+D
		e.startDirs()
	case 127, 8: // Backspace
		if e.pos > 0 {
			e.edit(func() { e.deleteRange(e.pos-1, e.pos) })
		}
	case 23: // Ctrl+W
		start := e.pos
		for start > 0 && isBlank(e.buf[start-1]) {
			start--
		}
		for start > 0 && !isBlank(e.buf[start-1]) {
			start--
		}
		e.edit(func() { e.deleteRange(start, e.pos) })
	case 21: // Ctrl+U
		e.edit(func() { e.deleteRange(e.lineStart(e.pos), e.pos) })
	case 11: // Ctrl+K
		e.edit(func() { e.deleteRange(e.pos, e.lineEnd(e.pos)) })
	case 1, keyHome: // Ctrl+A
		e.pos = e.lineStart(e.pos)
	case 5, keyEnd: // Ctrl+E
		e.pos = e.lineEnd(e.pos)
	case 2, keyLeft: // Ctrl+B
		if e.pos > 0 {
			e.pos--
		}
	case 6, keyRight: // Ctrl+F: take the suggestion at the end, else move right
		if !e.acceptHint() && e.pos < len(e.buf) {
			e.pos++
		}
	case 18: // Ctrl+R
		e.startSearch()
	case keyDel:
		e.edit(func() { e.deleteRange(e.pos, min(e.pos+1, len(e.buf))) })
	case 16, keyUp: // Ctrl+P
		e.lineUp(1)
	case 14, keyDown: // Ctrl+N
		e.lineDown(1)
	case 12: // Ctrl+L
		e.write("\x1b[H\x1b[2J")
		e.cursorRow = 0
	case keyPaste:
		e.edit(func() { e.insert(e.paste) })
	case '\t':
		e.tab()
	case ' ':
		e.edit(func() {
			e.expandAbbr(e.pos)
			e.insert(" ")
		})
	case 0: // Ctrl+Space: a space that does not expand an abbr
		e.edit(func() { e.insert(" ") })
	case '|':
		e.edit(func() {
			// An input can't start with a pipe: pipe the last input instead,
			// rerun but shown in full before Enter.
			if len(e.buf) == 0 && e.last != "" {
				e.insert(e.last + " ")
			}
			e.insert("|")
		})
	case keyUnknown, keyErr, keyWake:
	default:
		if k >= 32 {
			e.edit(func() {
				e.dedent(k)
				e.insert(string(k))
			})
		}
	}
	return actNone
}

// dedent removes one indent level when a closing bracket is the first
// char typed on a line.
func (e *editor) dedent(k rune) {
	if !strings.ContainsRune("}])", k) {
		return
	}
	ls := e.lineStart(e.pos)
	if e.firstNonBlank(ls) != e.pos || e.pos-ls < 4 {
		return
	}
	for _, r := range e.buf[e.pos-4 : e.pos] {
		if r != ' ' {
			return
		}
	}
	e.deleteRange(e.pos-4, e.pos)
}

// edit runs a change made in insert mode; the first one of an insert
// session takes the undo snapshot.
func (e *editor) edit(f func()) {
	if !e.insSnap {
		e.snapshot()
		e.insSnap = true
	}
	f()
}

// enter accepts the input, or starts a new line when it is unfinished.
func (e *editor) enter() action {
	if e.expandAbbr(len(e.buf)) {
		e.pos = len(e.buf)
	}
	if e.more == nil || !e.more(string(e.buf)) {
		return actAccept
	}
	if e.mode == normalMode {
		e.pos = len(e.buf)
		e.enterInsert()
	}
	ind := e.indent()
	e.edit(func() { e.insert("\n" + ind) })
	return actNone
}

// expandAbbr replaces the abbr ending at pos with its expansion.
func (e *editor) expandAbbr(pos int) bool {
	if e.abbr == nil {
		return false
	}
	start, text, ok := e.abbr(e.buf, pos)
	if !ok {
		return false
	}
	e.edit(func() {
		e.pos = pos
		e.replace(start, pos, text)
	})
	return true
}

// indent is the current line's indentation, one level deeper after an
// opening bracket.
func (e *editor) indent() string {
	ls := e.lineStart(e.pos)
	i := ls
	for i < len(e.buf) && (e.buf[i] == ' ' || e.buf[i] == '\t') {
		i++
	}
	ind := string(e.buf[ls:i])
	j := e.pos - 1
	for j >= ls && isBlank(e.buf[j]) {
		j--
	}
	if j >= ls && strings.ContainsRune("{[(", e.buf[j]) {
		ind += "    "
	}
	return ind
}

// ---- buffer helpers

func (e *editor) snapshot() {
	e.undo = append(e.undo, snapshot{append([]rune(nil), e.buf...), e.pos})
	e.redo = nil
}

func (e *editor) insert(s string) {
	r := []rune(s)
	e.buf = append(e.buf[:e.pos], append(r, e.buf[e.pos:]...)...)
	e.pos += len(r)
}

func (e *editor) deleteRange(a, b int) {
	if a >= b {
		return
	}
	e.buf = append(e.buf[:a], e.buf[b:]...)
	e.pos = a
}

func (e *editor) lineStart(p int) int {
	for p > 0 && e.buf[p-1] != '\n' {
		p--
	}
	return p
}

func (e *editor) lineEnd(p int) int {
	for p < len(e.buf) && e.buf[p] != '\n' {
		p++
	}
	return p
}

func (e *editor) lineIndex(p int) int {
	n := 0
	for _, r := range e.buf[:p] {
		if r == '\n' {
			n++
		}
	}
	return n
}

func (e *editor) lineCount() int { return e.lineIndex(len(e.buf)) + 1 }

// lineUp moves to the line above, or to older history on the first line.
func (e *editor) lineUp(n int) {
	for range n {
		ls := e.lineStart(e.pos)
		if ls == 0 {
			e.historyMove(-1)
			return
		}
		col := e.pos - ls
		ps := e.lineStart(ls - 1)
		e.pos = min(ps+col, ls-1)
	}
	e.clamp()
}

// lineDown moves to the line below, or to newer history on the last line.
func (e *editor) lineDown(n int) {
	for range n {
		le := e.lineEnd(e.pos)
		if le == len(e.buf) {
			e.historyMove(1)
			return
		}
		col := e.pos - e.lineStart(e.pos)
		e.pos = min(le+1+col, e.lineEnd(le+1))
	}
	e.clamp()
}

func (e *editor) historyMove(d int) {
	n := e.hidx + d
	if n < 0 || n >= len(e.hist) {
		return
	}
	e.hist[e.hidx] = string(e.buf)
	e.hidx = n
	e.buf = []rune(e.hist[n])
	e.pos = len(e.buf) // going up lands on the last line, going down on the first
	if d > 0 {
		e.pos = 0
	}
	e.undo, e.redo = nil, nil
	e.clamp()
}

// clamp keeps the cursor on a character in normal mode, as vi does.
func (e *editor) clamp() {
	e.pos = max(0, min(e.pos, len(e.buf)))
	if e.mode == normalMode && e.pos == e.lineEnd(e.pos) && e.pos > e.lineStart(e.pos) {
		e.pos--
	}
}

func isBlank(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }

// ---- drawing

// refresh redraws the whole input. Wrapped lines are accounted for so the
// cursor lands on the right row.
func (e *editor) refresh() {
	cols := termCols(int(e.out.Fd()))
	var b strings.Builder
	if e.cursorRow > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", e.cursorRow)
	}
	b.WriteString("\r\x1b[J")
	shape := "\x1b[6 q" // bar
	if e.mode == normalMode {
		shape = "\x1b[2 q" // block
	}
	if shape != e.shape {
		b.WriteString(shape)
		e.shape = shape
	}
	var spans []Span
	if e.color != nil {
		spans = e.color(e.buf)
	}
	if a, b := e.bracketPair(); a >= 0 {
		spans = overlay(overlay(spans, a, bracketStyle), b, bracketStyle)
	}
	row, curRow, curCol, off := 0, 0, 0, 0
	for i, line := range strings.Split(string(e.buf), "\n") {
		ln := []rune(line)
		p := e.prompt
		if i > 0 {
			p = e.cont
			b.WriteString("\r\n")
			row++
		}
		w := strWidth(p)
		b.WriteString(p)
		b.WriteString(colored(ln, off, spans))
		lw := runesWidth(ln)
		used := w + lw
		if off+len(ln) == len(e.buf) {
			if h := e.hint(); h != "" {
				avail := cols - (w+lw)%cols - 1 // keep the hint on this row
				h, _, more := strings.Cut(h, "\n")
				if more {
					h += " ⏎…"
				}
				if h = truncate(h, avail); h != "" {
					b.WriteString("\x1b[2m" + displayText([]rune(h)) + "\x1b[0m")
					used += strWidth(h)
				}
			}
		}
		if i == 0 && e.rprompt != "" {
			if rw := strWidth(e.rprompt); used+rw+1 < cols {
				fmt.Fprintf(&b, "\x1b[%dG%s", cols-rw, e.rprompt) // leave the last column free
			}
		}
		if (w+lw) > 0 && (w+lw)%cols == 0 {
			b.WriteString("\r\n") // leave the pending-wrap state on a full row
		}
		if e.pos >= off && e.pos <= off+len(ln) {
			cw := w + runesWidth(ln[:e.pos-off])
			curRow, curCol = row+cw/cols, cw%cols
		}
		row += (w + lw) / cols
		off += len(ln) + 1
	}
	below := e.belowLines(cols)
	if e.search != nil {
		below = e.searchLines(cols)
		curRow, curCol = row+1, strWidth(e.search.label())+runesWidth(e.search.query)
	}
	for _, line := range below {
		b.WriteString("\r\n" + line)
		row++
	}
	if row > curRow {
		fmt.Fprintf(&b, "\x1b[%dA", row-curRow)
	}
	b.WriteString("\r")
	if curCol > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", curCol)
	}
	e.cursorRow = curRow
	e.write(b.String())
}

// colored is line ln, which starts at off in the buffer, with the styles of
// the spans that cover it.
func colored(ln []rune, off int, spans []Span) string {
	var b strings.Builder
	i := 0
	for _, s := range spans {
		start, end := max(s.Start-off, i), min(s.End-off, len(ln))
		if start >= end {
			continue
		}
		b.WriteString(displayText(ln[i:start]))
		b.WriteString(s.Style + displayText(ln[start:end]) + "\x1b[0m")
		i = end
	}
	b.WriteString(displayText(ln[i:]))
	return b.String()
}

func displayText(rs []rune) string {
	var b strings.Builder
	for _, r := range rs {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case r < 32 || r == 127:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// strWidth is the display width of s; escape sequences (colors, OSC marks
// in a prompt) take no columns.
func strWidth(s string) int { return runesWidth([]rune(stripEscapes(s))) }

func stripEscapes(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '[': // CSI ... final byte
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
		case ']': // OSC ... BEL or ST
			j := i + 2
			for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == 0x1b {
				j++
			}
			i = j
		default:
			i++
		}
	}
	return b.String()
}

func runesWidth(rs []rune) int {
	w := 0
	for _, r := range rs {
		w += runeWidth(r)
	}
	return w
}

// runeWidth is 0 for combining marks, 2 for East Asian wide characters.
func runeWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r):
		return 0
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK, Hangul compatibility
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE4F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

const bracketStyle = "\x1b[1;4m"

// bracketPair is the bracket at the cursor, or the one just before it in
// insert mode, and the bracket it pairs with; -1, -1 if there is none.
func (e *editor) bracketPair() (int, int) {
	if e.pair == nil {
		return -1, -1
	}
	at := []int{e.pos}
	if e.mode == insertMode {
		at = append(at, e.pos-1)
	}
	for _, p := range at {
		if p >= 0 && p < len(e.buf) && strings.ContainsRune("()[]{}", e.buf[p]) {
			if m := e.pair(e.buf, p); m >= 0 {
				return p, m
			}
		}
	}
	return -1, -1
}

// overlay styles the rune at pos with style, cutting it out of the span
// that covers it; spans stay sorted and apart.
func overlay(spans []Span, pos int, style string) []Span {
	out := make([]Span, 0, len(spans)+2)
	added := false
	for _, s := range spans {
		if s.End <= pos || s.Start > pos {
			if !added && s.Start > pos {
				out = append(out, Span{pos, pos + 1, style})
				added = true
			}
			out = append(out, s)
			continue
		}
		if s.Start < pos {
			out = append(out, Span{s.Start, pos, s.Style})
		}
		out = append(out, Span{pos, pos + 1, style})
		added = true
		if pos+1 < s.End {
			out = append(out, Span{pos + 1, s.End, s.Style})
		}
	}
	if !added {
		out = append(out, Span{pos, pos + 1, style})
	}
	return out
}
