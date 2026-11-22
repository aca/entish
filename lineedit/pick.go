package lineedit

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Pick lets the user pick from items on the terminal (/dev/tty), as fzf
// does: an fzf query filters them, Tab marks several, Enter picks the
// marked ones, or the selected one if none is marked. It returns the
// indexes picked, in input order; none on Esc, interrupted on Ctrl+C.
func Pick(items []string) (picked []int, interrupted bool, err error) {
	terminalInput.Lock()
	defer terminalInput.Unlock()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, false, fmt.Errorf("no terminal: %w", err)
	}
	defer tty.Close()
	restore, err := makeRaw(int(tty.Fd()))
	if err != nil {
		return nil, false, err
	}
	defer restore()

	e := &editor{in: tty, out: tty}
	p := newPicker(items)
	visible := min(searchRows, max(0, termRows(int(tty.Fd()))-1))
	rows := min(visible, len(items)) + 1
	if rows > 1 {
		e.write(strings.Repeat("\r\n", rows-1) + fmt.Sprintf("\x1b[%dA", rows-1))
	}
	defer e.write("\r\x1b[J")
	for {
		e.write("\r\x1b[J" + p.drawRows(termCols(int(tty.Fd())), visible))
		k := e.readKey()
		if k == keyErr {
			return nil, false, e.readErr
		}
		if done, intr := p.key(k, e.paste); done {
			return p.picked(), intr, nil
		}
	}
}

type picker struct {
	items   []string
	query   []rune
	matches []int // indexes into items
	idx     int   // into matches
	marked  map[int]bool
	done    bool // Enter, not Esc
}

func newPicker(items []string) *picker {
	p := &picker{items: items, marked: map[int]bool{}}
	p.filter()
	return p
}

func (p *picker) filter() {
	q := ParseQuery(string(p.query))
	p.matches = p.matches[:0]
	for i, it := range p.items {
		if q.Match(it) {
			p.matches = append(p.matches, i)
		}
	}
	p.idx = 0
}

// key handles a key and reports whether picking is over, and whether by
// Ctrl+C.
func (p *picker) key(k rune, paste string) (done, interrupted bool) {
	switch k {
	case '\r', '\n':
		if len(p.marked) == 0 && len(p.matches) == 0 {
			return false, false
		}
		p.done = true
		return true, false
	case 3: // Ctrl+C
		return true, true
	case 27, 7: // Esc, Ctrl+G
		return true, false
	case '\t':
		if len(p.matches) > 0 {
			i := p.matches[p.idx]
			if p.marked[i] {
				delete(p.marked, i)
			} else {
				p.marked[i] = true
			}
			p.idx = min(p.idx+1, len(p.matches)-1)
		}
	case 127, 8:
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.filter()
		}
	case 23: // Ctrl+W
		q := strings.TrimRight(string(p.query), " ")
		if i := strings.LastIndex(q, " "); i >= 0 {
			p.query = []rune(q[:i+1])
		} else {
			p.query = nil
		}
		p.filter()
	case 21: // Ctrl+U
		p.query = nil
		p.filter()
	case 14, keyDown: // Ctrl+N
		if p.idx+1 < len(p.matches) {
			p.idx++
		}
	case 16, keyUp: // Ctrl+P
		if p.idx > 0 {
			p.idx--
		}
	case keyPaste:
		p.query = append(p.query, []rune(strings.ReplaceAll(paste, "\n", " "))...)
		p.filter()
	default:
		if k >= 32 {
			p.query = append(p.query, k)
			p.filter()
		}
	}
	return false, false
}

// picked is what Enter picked: the marked items, or the selected one.
func (p *picker) picked() []int {
	if !p.done {
		return nil
	}
	return p.selection()
}

// selection is also used to preview the current choice before Enter.
func (p *picker) selection() []int {
	if len(p.marked) == 0 {
		if len(p.matches) == 0 {
			return nil
		}
		return []int{p.matches[p.idx]}
	}
	var out []int
	for i := range p.marked {
		out = append(out, i)
	}
	slices.Sort(out)
	return out
}

// draw returns the query line and a window of matches around the
// selection, leaving the cursor at the end of the query.
func (p *picker) draw(cols int) string {
	return p.drawRows(cols, searchRows)
}

func (p *picker) drawRows(cols, rows int) string {
	lines, col := p.lines(cols, rows)
	out := strings.Join(lines, "\r\n")
	if len(lines) > 1 {
		out += fmt.Sprintf("\x1b[%dA", len(lines)-1)
	}
	return out + fmt.Sprintf("\r\x1b[%dC", col)
}

func (p *picker) lines(cols, rows int) ([]string, int) {
	head := truncateLeft("pick: "+displayText(p.query), max(1, cols-1))
	info := fmt.Sprintf("  %d/%d", len(p.matches), len(p.items))
	if len(p.marked) > 0 {
		info += fmt.Sprintf(" (%d marked)", len(p.marked))
	}
	lines := []string{head + "\x1b[2m" + truncate(info, max(0, cols-1-runesWidth([]rune(head)))) + "\x1b[0m"}
	first := max(0, p.idx-rows+1)
	for i := first; i < len(p.matches) && i < first+rows; i++ {
		it := p.matches[i]
		mark := " "
		if p.marked[it] {
			mark = "+"
		}
		text := truncate(displayText([]rune(strings.ReplaceAll(p.items[it], "\n", " ⏎ "))), cols-4)
		if i == p.idx {
			text = "\x1b[7m" + truncate(">"+mark+" "+text, max(1, cols-1)) + "\x1b[0m"
		} else {
			text = truncate(" "+mark+" "+text, max(1, cols-1))
		}
		lines = append(lines, text)
	}
	return lines, runesWidth([]rune(head))
}
