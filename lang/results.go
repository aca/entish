package lang

import (
	"fmt"
	"strconv"
)

// Keep a bounded number of inputs, not permanent lexical bindings: dropping
// a record releases its values unless a variable or closure still holds them.
const resultHistorySize = 100

type resultEntry struct {
	values *List
	failed bool
}

type resultHistory struct {
	last    int
	entries [resultHistorySize]resultEntry
}

// NextResult is the number shown in the next REPL prompt. Scripts, editor
// actions and blank input do not consume numbers; failed REPL inputs do.
func (in *Interp) NextResult() int { return in.results.last + 1 }

func (h *resultHistory) save(values []Value, err error) {
	h.last++
	e := resultEntry{failed: err != nil}
	if err == nil {
		e.values = &List{Items: values}
	}
	h.entries[(h.last-1)%resultHistorySize] = e
}

func (h *resultHistory) lookup(name string) (*List, error) {
	id, err := strconv.Atoi(name)
	if err != nil || id < 1 || id > h.last {
		return nil, fmt.Errorf("REPL result $%s is not available; use the number of a completed earlier input", name)
	}
	if h.last-id >= resultHistorySize {
		return nil, fmt.Errorf("REPL result $%s has expired; only the last %d inputs are retained; collect results with let saved = ($n |> list) to keep them", name, resultHistorySize)
	}
	e := h.entries[(id-1)%resultHistorySize]
	if e.failed {
		return nil, fmt.Errorf("REPL input #%d failed; its result is unavailable", id)
	}
	return e.values, nil
}
