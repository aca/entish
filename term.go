package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// termMarks writes the escape sequences modern terminals and tmux use to
// follow a shell:
//
//	OSC 2      window title: the directory at the prompt, the command while it runs
//	OSC 7      current directory (new tabs/panes open there; tmux pane_path)
//	OSC 133 A  prompt starts       B  input starts (end of the prompt)
//	OSC 133 C  output starts       D  command finished, with exit status
//
// Terminals that don't know them ignore them.
type termMarks struct {
	w        io.Writer
	cwd      func() string
	host     string
	running  bool
	started  time.Time
	lastCode atomic.Int64 // read by the direnv hook's goroutine
}

func newTermMarks(w io.Writer, cwd func() string) *termMarks {
	host, _ := os.Hostname()
	return &termMarks{w: w, cwd: cwd, host: host}
}

func osc133(s string) string { return "\x1b]133;" + s + "\x1b\\" }

func (t *termMarks) beforePrompt() {
	u := url.URL{Scheme: "file", Host: t.host, Path: t.cwd()}
	fmt.Fprint(t.w, title(tildePath(t.cwd()))+"\x1b]7;"+u.String()+"\x1b\\"+osc133("A"))
}

func (t *termMarks) commandStart(src string) {
	t.running, t.started = true, time.Now()
	line, _, more := strings.Cut(strings.TrimSpace(src), "\n")
	if more {
		line += " …"
	}
	fmt.Fprint(t.w, title(line)+osc133("C"))
}

// title sets the window title to s, without control chars, which could end
// the sequence early.
func title(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return -1
		}
		return r
	}, s)
	if rs := []rune(s); len(rs) > 80 {
		s = string(rs[:79]) + "…"
	}
	return "\x1b]2;" + s + "\x1b\\"
}

// slowCommand is how long a command must take before its duration is shown.
const slowCommand = 5 * time.Second

func (t *termMarks) commandDone(code int) {
	if !t.running {
		return
	}
	t.running = false
	t.lastCode.Store(int64(code))
	if d := time.Since(t.started); d >= slowCommand {
		fmt.Fprintf(os.Stderr, "\x1b[33mtook %s\x1b[0m\n", formatDuration(d))
	}
	fmt.Fprint(t.w, osc133(fmt.Sprintf("D;%d", code)))
}

// formatDuration: 5.2s, 1m02s, 1h02m03s.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	s := int(d.Round(time.Second).Seconds())
	if s < 3600 {
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", s/3600, s/60%60, s%60)
}

// Prompts in the style of nushell's defaults: the path in green with
// brighter separators and "> "; on the right [direnv] when direnv has an
// .envrc loaded (it sets DIRENV_DIR), the last failing exit code in red,
// then the date and time.
func leftPrompt(cwd string) string {
	cwd = tildePath(cwd)
	const path, sep, reset = "\x1b[1;32m", "\x1b[1;92m", "\x1b[0m"
	return path + strings.ReplaceAll(cwd, "/", sep+"/"+path) + reset + "> "
}

func rightPrompt(lastCode int, direnv bool, now time.Time) string {
	const val, sep, reset = "\x1b[35m", "\x1b[95m", "\x1b[0m"
	r := ""
	if direnv {
		r += "\x1b[36m[direnv]" + reset + " "
	}
	if lastCode != 0 {
		r += fmt.Sprintf("\x1b[1;31m%d%s ", lastCode, reset)
	}
	ts := now.Format("01/02/2006 03:04:05 PM")
	ts = strings.NewReplacer("/", sep+"/"+val, ":", sep+":"+val).Replace(ts)
	return r + val + ts + reset
}

// tildePath writes the home directory as ~.
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}
