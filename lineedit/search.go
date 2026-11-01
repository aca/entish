package lineedit

import (
	"os"
	"strings"
	"time"
)

// History search (Ctrl+R, or / in normal mode): an fzf query filters the
// history, newest first. Enter puts the selected entry into the buffer
// without running it; Esc or Ctrl+C restores the buffer.
//
// Location mode (Ctrl+D) is the same list over recent directories; Enter
// changes to the selected one and keeps the buffer.

const searchRows = 10

type searchState struct {
	query    []rune
	matches  []string // newest first
	idx      int
	saved    []rune
	savedPos int
	dirs     []Dir // location mode
	visited  map[string]time.Time
}

func (s *searchState) label() string {
	if s.visited != nil {
		return "location: "
	}
	return "history: "
}

func (e *editor) startDirs() {
	s := &searchState{saved: append([]rune(nil), e.buf...), savedPos: e.pos, visited: map[string]time.Time{}}
	if e.dirs != nil {
		s.dirs = e.dirs.Dirs()
	}
	for _, d := range s.dirs {
		s.visited[d.Path] = d.Time
	}
	e.search = s
	e.filterSearch()
}

func (e *editor) startSearch() {
	e.search = &searchState{
		query:    append([]rune(nil), e.buf...),
		saved:    append([]rune(nil), e.buf...),
		savedPos: e.pos,
	}
	e.filterSearch()
}

func (e *editor) filterSearch() {
	s := e.search
	q := ParseQuery(string(s.query))
	s.matches = s.matches[:0]
	for _, d := range s.dirs {
		if q.Match(homeDir(d.Path)) {
			s.matches = append(s.matches, d.Path)
		}
	}
	for i := len(e.history) - 1; s.visited == nil && i >= 0; i-- {
		if q.Match(e.history[i]) {
			s.matches = append(s.matches, e.history[i])
		}
	}
	s.idx = 0
}

func (e *editor) searchKey(k rune) {
	s := e.search
	switch k {
	case '\r', '\n', '\t':
		if s.visited != nil {
			e.search = nil
			if len(s.matches) > 0 {
				e.handOver(func() { e.dirs.Cd(s.matches[s.idx]) })
			}
			return
		}
		if len(s.matches) > 0 {
			e.snapshot()
			e.buf = []rune(s.matches[s.idx])
			e.pos = len(e.buf)
		}
		e.search, e.mode = nil, insertMode
	case 27, 3, 7: // Esc, Ctrl+C, Ctrl+G
		e.buf, e.pos = s.saved, s.savedPos
		e.search = nil
	case 127, 8:
		if len(s.query) > 0 {
			s.query = s.query[:len(s.query)-1]
			e.filterSearch()
		}
	case 23: // Ctrl+W
		q := strings.TrimRight(string(s.query), " ")
		if i := strings.LastIndex(q, " "); i >= 0 {
			s.query = []rune(q[:i+1])
		} else {
			s.query = nil
		}
		e.filterSearch()
	case 21: // Ctrl+U
		s.query = nil
		e.filterSearch()
	case 18, 14, keyDown: // Ctrl+R, Ctrl+N: older
		if s.idx+1 < len(s.matches) {
			s.idx++
		}
	case 16, keyUp: // Ctrl+P: newer
		if s.idx > 0 {
			s.idx--
		}
	case keyPaste:
		s.query = append(s.query, []rune(strings.ReplaceAll(e.paste, "\n", " "))...)
		e.filterSearch()
	default:
		if k >= 32 {
			s.query = append(s.query, k)
			e.filterSearch()
		}
	}
}

// searchLines draws the query line (where the cursor goes) and a window of
// matches around the selection.
func (e *editor) searchLines(cols int) []string {
	s := e.search
	head := s.label() + string(s.query)
	if len(s.matches) == 0 {
		head += "  \x1b[2m(no match)\x1b[0m"
	}
	out := []string{head}
	first := max(0, s.idx-searchRows+1)
	for i := first; i < len(s.matches) && i < first+searchRows; i++ {
		var when, dir string
		if s.visited != nil {
			out = append(out, searchRow(i == s.idx, s.visited[s.matches[i]].Format("01-02 15:04"), homeDir(s.matches[i]), "", cols))
			continue
		}
		if e.hstore != nil {
			if t, d, ok := e.hstore.Last(s.matches[i]); ok {
				when, dir = t.Format("01-02 15:04"), homeDir(d)
			}
		}
		cmd := strings.ReplaceAll(s.matches[i], "\n", " ⏎ ")
		out = append(out, searchRow(i == s.idx, when, cmd, dir, cols))
	}
	return out
}

// searchRow lays out a match on one row: the time it last ran on the left,
// its directory on the right, and the command between, cut to fit.
func searchRow(sel bool, when, cmd, dir string, cols int) string {
	w := cols - 1 // leave the last column free
	if w < 40 {
		when, dir = "", ""
	}
	if when != "" {
		when += "  "
	}
	dir = truncateLeft(dir, w/3)
	room := w - 2 - runesWidth([]rune(when)) - runesWidth([]rune(dir))
	if dir != "" {
		room-- // a space before the directory
	}
	cmd = truncate(cmd, room)
	pad := strings.Repeat(" ", max(0, w-2-runesWidth([]rune(when+cmd+dir))))
	if sel {
		return "\x1b[7m> " + when + cmd + pad + dir + "\x1b[0m"
	}
	return "  \x1b[2m" + when + "\x1b[0m" + cmd + pad + "\x1b[2m" + dir + "\x1b[0m"
}

// truncateLeft keeps the end of s, which matters most in a path.
func truncateLeft(s string, w int) string {
	rs := []rune(s)
	if runesWidth(rs) <= w {
		return s
	}
	for len(rs) > 0 && runesWidth(rs)+1 > w {
		rs = rs[1:]
	}
	return "…" + string(rs)
}

// homeDir writes the home directory as ~.
func homeDir(d string) string {
	if home, err := os.UserHomeDir(); err == nil && (d == home || strings.HasPrefix(d, home+"/")) {
		return "~" + d[len(home):]
	}
	return d
}
