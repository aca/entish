package lineedit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// Navigate browses directories on /dev/tty, in a panel below the cursor.
// Enter accepts the displayed directory; cancel returns an empty path.
// Browsing never changes process cwd.
func Navigate(start string) (dir string, interrupted bool, err error) {
	n, err := newNavigator(start)
	if err != nil {
		return "", false, err
	}
	terminalInput.Lock()
	defer terminalInput.Unlock()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", false, fmt.Errorf("no terminal: %w", err)
	}
	defer tty.Close()
	r, w, err := os.Pipe()
	if err != nil {
		return "", false, err
	}
	defer r.Close()
	defer w.Close()
	e := &editor{in: tty, out: tty, wakeR: r, wakeW: w}
	defer onResize(e.wakeUp)()
	restore, err := makeRaw(int(tty.Fd()))
	if err != nil {
		return "", false, err
	}
	defer restore()
	rows := navRows(termRows(int(tty.Fd())))
	e.write(strings.Repeat("\r\n", rows-1) + fmt.Sprintf("\x1b[%dA", rows-1)) // scroll to make room
	e.write("\x1b[?25l\x1b[?2004h")
	defer e.write("\r\x1b[J\x1b[0m\x1b[?2004l\x1b[?25h")
	for {
		lines := n.draw(termCols(int(tty.Fd())), min(rows, navRows(termRows(int(tty.Fd())))))
		e.write("\r\x1b[J" + strings.Join(lines, "\r\n"))
		if len(lines) > 1 {
			fmt.Fprintf(tty, "\x1b[%dA", len(lines)-1)
		}
		e.write("\r")
		k := e.readKey()
		if k == keyErr {
			return "", false, e.readErr
		}
		if done, accept, intr := n.key(k); done {
			if accept {
				return n.path, false, nil
			}
			return "", intr, nil
		}
	}
}

// navRows is the height of the navigator: half the terminal, like
// fzf --height=50%, but at least 6 rows when the terminal has them.
func navRows(term int) int { return max(1, min(term-1, max(6, term/2))) }

type navEntry struct {
	name string
	dir  bool
	link bool
}

func (e navEntry) label() string {
	s := e.name
	if e.dir {
		s += "/"
	}
	if e.link {
		s += "@"
	}
	return s
}

type navigator struct {
	path      string
	entries   []navEntry
	parent    []navEntry
	parentIdx int
	matches   []navEntry
	idx       int
	hidden    bool
	filtering bool
	query     []rune
	message   string
	preview   []string
	positions map[string]string
}

func newNavigator(start string) (*navigator, error) {
	path, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	n := &navigator{positions: map[string]string{}}
	return n, n.move(path)
}

func navReadDir(path string, hidden bool) ([]navEntry, error) {
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	entries := make([]navEntry, 0, len(items))
	for _, item := range items {
		if !hidden && strings.HasPrefix(item.Name(), ".") {
			continue
		}
		e := navEntry{name: item.Name(), dir: item.IsDir(), link: item.Type()&os.ModeSymlink != 0}
		if e.link {
			info, err := os.Stat(filepath.Join(path, e.name))
			e.dir = err == nil && info.IsDir()
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		return entries[i].name < entries[j].name
	})
	return entries, nil
}

func (n *navigator) selected() string {
	if len(n.matches) == 0 {
		return ""
	}
	return n.matches[n.idx].name
}

func (n *navigator) move(path string) error {
	entries, err := navReadDir(path, n.hidden)
	if err != nil {
		return err
	}
	if name := n.selected(); name != "" {
		n.positions[n.path] = name
	}
	if path != n.path && path == filepath.Dir(n.path) {
		n.positions[path] = filepath.Base(n.path)
	}
	n.path, n.entries = path, entries
	n.query, n.filtering, n.message = nil, false, ""
	n.parent, _ = navReadDir(filepath.Dir(path), n.hidden)
	n.parentIdx = -1
	for i, entry := range n.parent {
		if entry.name == filepath.Base(path) {
			n.parentIdx = i
		}
	}
	n.filter(n.positions[path])
	return nil
}

func (n *navigator) filter(selected string) {
	n.matches, n.idx = nil, 0
	q := ParseQuery(string(n.query))
	for _, entry := range n.entries {
		if q.Match(entry.name) {
			n.matches = append(n.matches, entry)
			if entry.name == selected {
				n.idx = len(n.matches) - 1
			}
		}
	}
	n.updatePreview()
}

func (n *navigator) key(k rune) (done, accept, interrupted bool) {
	if k == 3 {
		return true, false, true
	}
	if n.filtering {
		switch k {
		case 27, 7:
			n.query, n.filtering = nil, false
		case '\r', '\n':
			n.filtering = false
			return
		case 127, 8:
			if len(n.query) > 0 {
				n.query = n.query[:len(n.query)-1]
			}
		case 21:
			n.query = nil
		default:
			if k < 32 || !unicode.IsPrint(k) {
				return
			}
			n.query = append(n.query, k)
		}
		n.filter("")
		return
	}
	n.message = ""
	var err error
	switch k {
	case 27, 7, 'q':
		return true, false, false
	case '\r', '\n':
		return true, true, false
	case 'j', keyDown, 14:
		n.idx = min(n.idx+1, max(0, len(n.matches)-1))
	case 'k', keyUp, 16:
		n.idx = max(0, n.idx-1)
	case 'g', keyHome:
		n.idx = 0
	case 'G', keyEnd:
		n.idx = max(0, len(n.matches)-1)
	case 'h', keyLeft, 127, 8:
		err = n.move(filepath.Dir(n.path))
	case 'l', keyRight:
		if len(n.matches) > 0 {
			if n.matches[n.idx].dir {
				err = n.move(filepath.Join(n.path, n.selected()))
			} else {
				n.message = "Not a directory"
			}
		}
	case '.':
		n.hidden = !n.hidden
		err = n.move(n.path)
		if err != nil {
			n.hidden = !n.hidden
		}
	case '/':
		n.filtering = true
	case 12: // Ctrl+L: reload
		err = n.move(n.path)
	case keyWake:
		return
	default:
		return
	}
	if err != nil {
		n.message = err.Error()
	}
	n.updatePreview()
	return
}

func (n *navigator) updatePreview() {
	n.preview = nil
	name := n.selected()
	if name == "" {
		return
	}
	path := filepath.Join(n.path, name)
	if n.matches[n.idx].dir {
		entries, err := navReadDir(path, n.hidden)
		if err != nil {
			n.preview = []string{err.Error()}
			return
		}
		for _, entry := range entries {
			n.preview = append(n.preview, entry.label())
		}
		if len(entries) == 0 {
			n.preview = []string{"(empty directory)"}
		}
		return
	}
	n.preview = navPreview(path)
}

// Only preview regular files, with a bounded read. Nonblocking open also
// handles a regular file replaced by a FIFO between Stat and Open.
func navPreview(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return []string{err.Error()}
	}
	if !info.Mode().IsRegular() {
		return []string{"(special file: no preview)"}
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return []string{err.Error()}
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return []string{err.Error()}
	}
	if !info.Mode().IsRegular() {
		return []string{"(special file: no preview)"}
	}
	data, err := io.ReadAll(io.LimitReader(f, 16*1024))
	if err != nil {
		return []string{err.Error()}
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return []string{fmt.Sprintf("(binary file, %d bytes)", info.Size())}
	}
	return strings.Split(string(data), "\n")
}

// navText makes filenames and previews inert terminal text and clips in one
// pass, including very long lines and wide characters.
func navText(s string, width int) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsPrint(r) {
			r = '?'
		}
		w := runeWidth(r)
		if w > width {
			break
		}
		width -= w
		b.WriteRune(r)
	}
	return b.String()
}

func navCell(s string, width int, selected bool) string {
	s = navText(s, width)
	s += strings.Repeat(" ", max(0, width-runesWidth([]rune(s))))
	if selected {
		return "\x1b[7m" + s + "\x1b[0m"
	}
	return s
}

func navWindow(entries []navEntry, idx, height int) ([]string, int) {
	first := max(0, idx-height/2)
	first = min(first, max(0, len(entries)-height))
	var lines []string
	for i := first; i < len(entries) && len(lines) < height; i++ {
		lines = append(lines, entries[i].label())
	}
	return lines, idx - first
}

// draw returns at most rows lines.
func (n *navigator) draw(cols, rows int) []string {
	width, height := max(1, cols-1), max(0, rows-4)
	var lines []string
	lines = append(lines, navText("nav: "+n.path, width))
	parent, pi := navWindow(n.parent, n.parentIdx, height)
	current, ci := navWindow(n.matches, n.idx, height)
	if len(current) == 0 {
		current, ci = []string{"(empty)"}, -1
	}
	cell := func(list []string, row, w, selected int) string {
		s := ""
		if row < len(list) {
			s = list[row]
		}
		return navCell(s, w, row == selected)
	}
	if cols >= 60 {
		left := (width - 6) / 4
		middle := (width - 6) / 3
		right := width - 6 - left - middle
		lines = append(lines, navCell("Parent", left, false)+" | "+navCell("Current", middle, false)+" | "+navCell("Preview", right, false))
		for row := 0; row < height; row++ {
			lines = append(lines, cell(parent, row, left, pi)+" | "+cell(current, row, middle, ci)+" | "+cell(n.preview, row, right, -1))
		}
	} else {
		lines = append(lines, navText("Current", width))
		for row := 0; row < height; row++ {
			lines = append(lines, cell(current, row, width, ci))
		}
	}
	status := fmt.Sprintf("%d entries  hidden:%t", len(n.matches), n.hidden)
	if n.filtering || len(n.query) > 0 {
		status = "/" + string(n.query) + "  (Enter: finish filter, Esc: clear)"
	}
	if n.message != "" {
		status = n.message
	}
	lines = append(lines, navText(status, width), navText("h/j/k/l arrows: move  Enter: cd here  /: filter  .: hidden  q/Esc: cancel", width))
	return lines[:min(len(lines), max(1, rows))]
}

// startNav opens the navigator (Ctrl+N) below the input, at the shell's
// cwd. The input stays as it is; Enter changes to the displayed directory.
func (e *editor) startNav() {
	if e.dirs == nil {
		return
	}
	n, err := newNavigator(e.dirs.Cwd())
	if err != nil {
		return
	}
	e.cancelCompletion()
	e.clearSuggestion()
	e.nav = n
}

func (e *editor) navKey(k rune) {
	done, accept, _ := e.nav.key(k)
	if !done {
		return
	}
	dir := e.nav.path
	e.nav = nil
	if accept {
		e.handOver(func() { e.dirs.Cd(dir) })
	}
}
