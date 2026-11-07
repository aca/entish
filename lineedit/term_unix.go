//go:build linux || darwin

package lineedit

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	return err == nil
}

// sane is the terminal state at the first makeRaw, after the rc file ran.
// Like fish and zsh's ttyctl -f, every prompt starts from it and commands
// get it back, so modes a crashed program left behind (no echo, no line
// editing) do not need stty sane.
var sane *unix.Termios

// makeRaw switches fd to raw mode the way linenoise does and returns a
// func that restores sane.
func makeRaw(fd int) (restore func(), err error) {
	if sane == nil {
		if sane, err = unix.IoctlGetTermios(fd, ioctlGetTermios); err != nil {
			return nil, err
		}
	}
	raw := *sane
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &raw); err != nil {
		return nil, err
	}
	return func() { unix.IoctlSetTermios(fd, ioctlSetTermios, sane) }, nil
}

func termCols(fd int) int {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return 80
	}
	return int(ws.Col)
}

// waitInput reports whether fd becomes readable within d. It tells a lone
// Esc from the start of an escape sequence (arrow keys).
func waitInput(fd int, d time.Duration) bool {
	return waitAny([]int{fd}, d)[0]
}

// waitAny waits until one of fds is readable, or d passes (d < 0: no
// limit), and reports which are readable.
func waitAny(fds []int, d time.Duration) []bool {
	ready := make([]bool, len(fds))
	for {
		var set unix.FdSet
		maxfd := 0
		for _, fd := range fds {
			set.Set(fd)
			maxfd = max(maxfd, fd)
		}
		var tv *unix.Timeval
		if d >= 0 {
			t := unix.NsecToTimeval(d.Nanoseconds())
			tv = &t
		}
		n, err := unix.Select(maxfd+1, &set, nil, nil, tv)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return ready
		}
		for i, fd := range fds {
			ready[i] = set.IsSet(fd)
		}
		return ready
	}
}

// onResize calls f whenever the terminal is resized, until stop is called.
func onResize(f func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				f()
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
