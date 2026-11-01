//go:build !linux && !darwin

package lineedit

import (
	"errors"
	"time"
)

// Only Linux and macOS have raw-mode support for now; elsewhere New falls back to a
// plain line reader.

func isTerminal(int) bool { return false }

func makeRaw(int) (func(), error) { return nil, errors.New("raw mode is not supported on this OS") }

func termCols(int) int { return 80 }

func onResize(func()) (stop func()) { return func() {} }

func waitInput(int, time.Duration) bool { return false }

func waitAny(fds []int, _ time.Duration) []bool {
	ready := make([]bool, len(fds))
	ready[0] = true
	return ready
}
