package lineedit

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Pipeline stages can request the controlling terminal concurrently. One
// reader owns its input and terminal modes until it has restored them.
var terminalInput sync.Mutex

// Prompt reads one literal line from the controlling terminal.
func Prompt(question string) (line string, interrupted bool, err error) {
	terminalInput.Lock()
	defer terminalInput.Unlock()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", false, fmt.Errorf("no terminal: %w", err)
	}
	defer tty.Close()
	restore, err := makeRaw(int(tty.Fd()))
	if err != nil {
		return "", false, err
	}
	defer restore()
	defer tty.WriteString("\x1b[0 q")

	// Only the final line of the question is redrawn as the user edits.
	start := strings.LastIndexByte(question, '\n') + 1
	e := &editor{in: tty, out: tty, prompt: question[start:]}
	e.write(strings.ReplaceAll(question[:start], "\n", "\r\n"))
	for {
		e.refresh()
		k := e.readKey()
		switch k {
		case keyErr:
			return "", false, e.readErr
		case 3: // Ctrl+C
			e.finish("^C")
			return "", true, nil
		case 4: // Ctrl+D
			if len(e.buf) == 0 {
				e.finish("")
				return "", false, io.EOF
			}
			fallthrough
		case '\r', '\n':
			e.finish("")
			return string(e.buf), false, nil
		case keyHome, keyEnd, keyLeft, keyRight, keyDel, 1, 2, 5, 6, 8, 11, 21, 23, 127:
			e.insertKey(k)
		default:
			if k == '\t' || k >= ' ' {
				e.insert(string(k))
			}
		}
	}
}
