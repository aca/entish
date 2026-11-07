package lineedit

import (
	"os"
	"os/exec"
	"strings"
	"unicode"
)

// Normal mode: a practical subset of vi.
//
//	motions     h l 0 ^ $ w b e W B E f F t T ; , (with counts)
//	operators   d c y + motion, text object (iw aw i" a" i' a' i( a( i[ i{ ...), dd cc yy
//	edits       x X s S C D Y r ~ p P J, i a I A o O
//	other       u Ctrl+R . j k (lines, then history), / (history search), Ctrl+D (recent dirs), v (open $VISUAL / $EDITOR), Enter

func (e *editor) normalKey(k rune) action {
	e.recording, e.rec = true, []rune{k}
	n, k := e.readCount(k)
	changed := false
	act := actNone
	switch k {
	case 3: // Ctrl+C
		act = actInterrupt
	case 4: // Ctrl+D
		e.startDirs()
	case '\r', '\n':
		act = e.enter()
	case 'i':
		e.enterInsert()
		changed = true
	case 'a':
		e.enterInsert()
		if e.pos < e.lineEnd(e.pos) {
			e.pos++
		}
		changed = true
	case 'I':
		e.enterInsert()
		e.pos = e.firstNonBlank(e.lineStart(e.pos))
		changed = true
	case 'A':
		e.enterInsert()
		e.pos = e.lineEnd(e.pos)
		changed = true
	case 'o':
		e.enterInsert()
		ind := e.leadingBlank(e.lineStart(e.pos))
		e.pos = e.lineEnd(e.pos)
		e.insert("\n" + ind)
		changed = true
	case 'O':
		e.enterInsert()
		ls := e.lineStart(e.pos)
		ind := e.leadingBlank(ls)
		e.pos = ls
		e.insert(ind + "\n")
		e.pos = ls + len([]rune(ind))
		changed = true
	case 'x', keyDel:
		changed = e.apply('d', e.pos, min(e.pos+n, e.lineEnd(e.pos)))
	case 'X':
		changed = e.apply('d', max(e.lineStart(e.pos), e.pos-n), e.pos)
	case 's':
		if e.pos == e.lineEnd(e.pos) {
			e.enterInsert()
			changed = true
		} else {
			changed = e.apply('c', e.pos, min(e.pos+n, e.lineEnd(e.pos)))
		}
	case 'S':
		changed = e.opLines('c', n)
	case 'C':
		e.enterInsert()
		e.yankDelete(e.pos, e.lineEnd(e.pos))
		changed = true
	case 'D':
		changed = e.apply('d', e.pos, e.lineEnd(e.pos))
	case 'Y':
		e.opLines('y', n)
	case 'r':
		ch := e.next()
		if ch >= 32 && e.pos+n <= e.lineEnd(e.pos) {
			e.change()
			for i := range n {
				e.buf[e.pos+i] = ch
			}
			e.pos += n - 1
			changed = true
		}
	case '~':
		if e.pos < e.lineEnd(e.pos) {
			e.change()
			for i := 0; i < n && e.pos < e.lineEnd(e.pos); i++ {
				r := e.buf[e.pos]
				if unicode.IsUpper(r) {
					e.buf[e.pos] = unicode.ToLower(r)
				} else {
					e.buf[e.pos] = unicode.ToUpper(r)
				}
				e.pos++
			}
			changed = true
		}
	case 'p', 'P':
		if e.reg != nil {
			e.change()
			e.put(k == 'p', n)
			changed = true
		}
	case 'J':
		changed = e.join(max(n, 2))
	case 'u':
		e.undoStep(&e.undo, &e.redo)
	case 18: // Ctrl+R
		e.undoStep(&e.redo, &e.undo)
	case '.':
		e.replay = append(append([]rune(nil), e.lastChange...), e.replay...)
	case 'v':
		e.openEditor()
	case '/':
		e.startSearch()
	case 'd', 'c', 'y':
		changed = e.operator(k, n)
	case 'j', keyDown, 14:
		e.lineDown(n)
	case 'k', keyUp, 16:
		e.lineUp(n)
	case keyPaste:
		e.enterInsert()
		e.insert(e.paste)
		e.mode = normalMode
		changed = true
	default:
		if m, ok := e.motion(k, n); ok {
			e.pos = m.pos
		}
	}
	switch {
	case !changed:
		e.recording = false
	case e.mode == normalMode:
		e.lastChange, e.recording = e.rec, false
	} // a change that entered insert mode is recorded until Esc
	e.clamp()
	return act
}

func (e *editor) readCount(k rune) (int, rune) {
	if k < '1' || k > '9' {
		return 1, k
	}
	n := 0
	for k >= '0' && k <= '9' {
		n = n*10 + int(k-'0')
		k = e.next()
	}
	return n, k
}

// change starts a vi change: one undo step, which also covers the insert
// session it may start.
func (e *editor) change() {
	e.snapshot()
	e.insSnap = true
}

func (e *editor) enterInsert() {
	e.change()
	e.mode = insertMode
}

func (e *editor) undoStep(from, to *[]snapshot) {
	if len(*from) == 0 {
		return
	}
	s := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, snapshot{append([]rune(nil), e.buf...), e.pos})
	e.buf, e.pos = s.buf, s.pos
}

// ---- motions

type move struct {
	pos  int
	incl bool // the motion includes its target char (e, f, t)
}

func (e *editor) motion(k rune, n int) (move, bool) {
	p := e.pos
	ls, le := e.lineStart(p), e.lineEnd(p)
	switch k {
	case 'h', keyLeft, 127, 8:
		return move{pos: max(ls, p-n)}, true
	case 'l', keyRight, ' ':
		return move{pos: min(le, p+n)}, true
	case '0', keyHome:
		return move{pos: ls}, true
	case '^':
		return move{pos: e.firstNonBlank(ls)}, true
	case '$', keyEnd:
		return move{pos: le}, true
	case 'w', 'W':
		for range n {
			p = e.wordForward(p, k == 'W')
		}
		return move{pos: p}, true
	case 'b', 'B':
		for range n {
			p = e.wordBack(p, k == 'B')
		}
		return move{pos: p}, true
	case 'e', 'E':
		for range n {
			p = e.wordEnd(p, k == 'E')
		}
		return move{pos: p, incl: true}, true
	case 'f', 'F', 't', 'T':
		ch := e.next()
		e.lastFind = [2]rune{k, ch}
		return e.find(k, ch, n)
	case ';', ',':
		cmd := e.lastFind[0]
		if cmd == 0 {
			return move{}, false
		}
		if k == ',' {
			cmd = map[rune]rune{'f': 'F', 'F': 'f', 't': 'T', 'T': 't'}[cmd]
		}
		return e.find(cmd, e.lastFind[1], n)
	}
	return move{}, false
}

func (e *editor) find(cmd, ch rune, n int) (move, bool) {
	ls, le := e.lineStart(e.pos), e.lineEnd(e.pos)
	i := e.pos
	if cmd == 'f' || cmd == 't' {
		for range n {
			i++
			for i < le && e.buf[i] != ch {
				i++
			}
			if i >= le {
				return move{}, false
			}
		}
		if cmd == 't' {
			i--
		}
		return move{pos: i, incl: true}, true
	}
	for range n {
		i--
		for i >= ls && e.buf[i] != ch {
			i--
		}
		if i < ls {
			return move{}, false
		}
	}
	if cmd == 'T' {
		i++
	}
	return move{pos: i}, true
}

// class: 0 blank, 1 word, 2 punctuation; big words (W B E) have no 2.
func (e *editor) class(i int, big bool) int {
	r := e.buf[i]
	switch {
	case isBlank(r):
		return 0
	case big, r == '_', unicode.IsLetter(r), unicode.IsDigit(r):
		return 1
	}
	return 2
}

func (e *editor) wordForward(p int, big bool) int {
	n := len(e.buf)
	if p >= n {
		return n
	}
	if c := e.class(p, big); c != 0 {
		for p < n && e.class(p, big) == c {
			p++
		}
	}
	for p < n && e.class(p, big) == 0 {
		p++
	}
	return p
}

func (e *editor) wordBack(p int, big bool) int {
	if p == 0 {
		return 0
	}
	p--
	for p > 0 && e.class(p, big) == 0 {
		p--
	}
	c := e.class(p, big)
	for p > 0 && e.class(p-1, big) == c {
		p--
	}
	return p
}

func (e *editor) wordEnd(p int, big bool) int {
	n := len(e.buf)
	if n == 0 {
		return 0
	}
	p++
	for p < n && e.class(p, big) == 0 {
		p++
	}
	if p >= n {
		return n - 1
	}
	c := e.class(p, big)
	for p+1 < n && e.class(p+1, big) == c {
		p++
	}
	return p
}

func (e *editor) firstNonBlank(ls int) int {
	i := ls
	for i < len(e.buf) && (e.buf[i] == ' ' || e.buf[i] == '\t') {
		i++
	}
	return i
}

func (e *editor) leadingBlank(ls int) string { return string(e.buf[ls:e.firstNonBlank(ls)]) }

// ---- operators

func (e *editor) operator(op rune, n int) bool {
	n2, k := e.readCount(e.next())
	total := n * n2
	switch {
	case k == op: // dd cc yy
		return e.opLines(op, total)
	case k == 'j' || k == keyDown || k == 'k' || k == keyUp:
		line := e.lineIndex(e.pos)
		other := line + total
		if k == 'k' || k == keyUp {
			other = line - total
		}
		if other < 0 || other >= e.lineCount() {
			return false
		}
		return e.opLineRange(op, min(line, other), max(line, other))
	case k == 'i' || k == 'a':
		a, b, ok := e.textObject(k == 'a', e.next())
		return ok && e.apply(op, a, b)
	}
	if op == 'c' && (k == 'w' || k == 'W') && e.pos < len(e.buf) && !isBlank(e.buf[e.pos]) {
		// cw changes to the end of the word, like ce, but a one-char word
		// is just that char
		big := k == 'W'
		end, c := e.pos, e.class(e.pos, big)
		for end < len(e.buf) && e.class(end, big) == c {
			end++
		}
		for range total - 1 {
			end = e.wordEnd(end-1, big) + 1
		}
		return e.apply(op, e.pos, end)
	}
	m, ok := e.motion(k, total)
	if !ok {
		return false
	}
	a, b := min(e.pos, m.pos), max(e.pos, m.pos)
	if m.incl {
		b = min(b+1, len(e.buf))
	}
	if k == 'w' || k == 'W' {
		for i := a; i < b; i++ { // dw at the end of a line keeps the newline
			if e.buf[i] == '\n' && i > a {
				b = i
				break
			}
		}
	}
	return e.apply(op, a, b)
}

// apply runs a charwise operator on [a, b). It reports whether the buffer
// changed.
func (e *editor) apply(op rune, a, b int) bool {
	if a >= b {
		return false
	}
	e.reg, e.regLines = append([]rune(nil), e.buf[a:b]...), false
	switch op {
	case 'y':
		e.pos = a
		return false
	case 'c':
		e.enterInsert()
	default:
		e.change()
	}
	e.deleteRange(a, b)
	return true
}

func (e *editor) yankDelete(a, b int) {
	if a < b {
		e.reg, e.regLines = append([]rune(nil), e.buf[a:b]...), false
		e.deleteRange(a, b)
	}
}

func (e *editor) opLines(op rune, n int) bool {
	first := e.lineIndex(e.pos)
	return e.opLineRange(op, first, min(first+n-1, e.lineCount()-1))
}

// opLineRange runs a linewise operator on lines first..last.
func (e *editor) opLineRange(op rune, first, last int) bool {
	s := e.lineStartOf(first)
	le := e.lineEnd(e.lineStartOf(last))
	e.reg, e.regLines = append([]rune(nil), e.buf[s:le]...), true
	switch op {
	case 'y':
		return false
	case 'c':
		e.enterInsert()
		ind := e.leadingBlank(s)
		e.deleteRange(s, le)
		e.insert(ind)
		return true
	}
	e.change()
	a, b := s, le
	if b < len(e.buf) {
		b++
	} else if a > 0 {
		a--
	}
	e.deleteRange(a, b)
	e.pos = e.firstNonBlank(e.lineStart(min(a, len(e.buf))))
	return true
}

func (e *editor) lineStartOf(line int) int {
	p := 0
	for i := 0; i < line; i++ {
		p = e.lineEnd(p) + 1
	}
	return p
}

func (e *editor) put(after bool, n int) {
	if e.regLines {
		text := strings.Repeat(string(e.reg)+"\n", n)
		if after {
			le := e.lineEnd(e.pos)
			e.pos = le
			e.insert("\n" + strings.TrimSuffix(text, "\n"))
			e.pos = le + 1
		} else {
			ls := e.lineStart(e.pos)
			e.pos = ls
			e.insert(text)
			e.pos = ls
		}
		return
	}
	if after && e.pos < e.lineEnd(e.pos) {
		e.pos++
	}
	e.insert(strings.Repeat(string(e.reg), n))
	e.pos--
}

func (e *editor) join(lines int) bool {
	changed := false
	for range lines - 1 {
		le := e.lineEnd(e.pos)
		if le == len(e.buf) {
			break
		}
		if !changed {
			e.change()
			changed = true
		}
		j := e.firstNonBlank(le + 1)
		sep := " "
		if le == e.lineStart(le) || j == e.lineEnd(j) {
			sep = ""
		}
		e.pos = le
		e.deleteRange(le, j)
		e.insert(sep)
		e.pos = le
	}
	return changed
}

// ---- text objects

func (e *editor) textObject(around bool, obj rune) (int, int, bool) {
	switch obj {
	case 'w', 'W':
		return e.wordObject(around, obj == 'W')
	case '"', '\'', '`':
		return e.quoteObject(around, obj)
	}
	pairs := map[rune][2]rune{
		'(': {'(', ')'}, ')': {'(', ')'}, 'b': {'(', ')'},
		'[': {'[', ']'}, ']': {'[', ']'},
		'{': {'{', '}'}, '}': {'{', '}'}, 'B': {'{', '}'},
		'<': {'<', '>'}, '>': {'<', '>'},
	}
	if p, ok := pairs[obj]; ok {
		return e.bracketObject(around, p[0], p[1])
	}
	return 0, 0, false
}

func (e *editor) wordObject(around, big bool) (int, int, bool) {
	n := len(e.buf)
	if n == 0 {
		return 0, 0, false
	}
	p := min(e.pos, n-1)
	c := e.class(p, big)
	a, b := p, p+1
	for a > 0 && e.class(a-1, big) == c && e.buf[a-1] != '\n' {
		a--
	}
	for b < n && e.class(b, big) == c && e.buf[b] != '\n' {
		b++
	}
	if around {
		if b < n && (e.buf[b] == ' ' || e.buf[b] == '\t') {
			for b < n && (e.buf[b] == ' ' || e.buf[b] == '\t') {
				b++
			}
		} else {
			for a > 0 && (e.buf[a-1] == ' ' || e.buf[a-1] == '\t') {
				a--
			}
		}
	}
	return a, b, true
}

func (e *editor) quoteObject(around bool, q rune) (int, int, bool) {
	ls, le := e.lineStart(e.pos), e.lineEnd(e.pos)
	var qs []int
	for i := ls; i < le; i++ {
		if e.buf[i] == q {
			qs = append(qs, i)
		}
	}
	for i := 0; i+1 < len(qs); i += 2 {
		open, close := qs[i], qs[i+1]
		if e.pos <= close { // the pair containing the cursor, or the next one
			if around {
				return open, close + 1, true
			}
			return open + 1, close, true
		}
	}
	return 0, 0, false
}

func (e *editor) bracketObject(around bool, open, close rune) (int, int, bool) {
	n := len(e.buf)
	start := -1
	depth := 0
	i := min(e.pos, n-1)
	if i >= 0 && e.buf[i] == close {
		i--
	}
	for ; i >= 0; i-- {
		switch e.buf[i] {
		case close:
			depth++
		case open:
			if depth == 0 {
				start = i
			}
			depth--
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	depth = 0
	for j := start + 1; j < n; j++ {
		switch e.buf[j] {
		case open:
			depth++
		case close:
			if depth == 0 {
				if around {
					return start, j + 1, true
				}
				return start + 1, j, true
			}
			depth--
		}
	}
	return 0, 0, false
}

// ---- external editor

// openEditor edits the input in $VISUAL / $EDITOR (e.g. nvim with the
// user's own config) and puts the result back into the buffer.
func (e *editor) openEditor() {
	f, err := os.CreateTemp("", "entish-*.ent")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	f.WriteString(string(e.buf) + "\n")
	f.Close()

	e.pos = len(e.buf)
	e.refresh()
	e.write("\r\n\x1b[?2004l\x1b[0 q")
	e.cursorRow, e.shape = 0, ""
	e.restore()

	ed := strings.Fields(os.Getenv("VISUAL"))
	if len(ed) == 0 {
		ed = strings.Fields(os.Getenv("EDITOR"))
	}
	if len(ed) == 0 {
		ed = []string{"vi"}
	}
	cmd := exec.Command(ed[0], append(ed[1:], f.Name())...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = e.in, e.out, os.Stderr
	runErr := cmd.Run()

	if restore, err := makeRaw(int(e.in.Fd())); err == nil {
		e.restore = restore
	}
	e.write("\x1b[?2004h")
	data, err := os.ReadFile(f.Name())
	if runErr != nil || err != nil {
		return
	}
	e.change()
	e.buf = []rune(strings.TrimRight(string(data), "\n"))
	e.pos = len(e.buf)
	e.mode = normalMode
}
