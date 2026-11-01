package lineedit

import (
	"context"
	"os"
	"testing"
	"time"
)

// A slow completer must not hold up typing, and its result applies once it
// arrives if the buffer is unchanged.
func TestAsyncCompletion(t *testing.T) {
	inR, inW, _ := os.Pipe()
	release := make(chan struct{})
	slow := func(ctx context.Context, buf []rune, pos int) (Completion, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return Completion{}, ctx.Err()
		}
		return Completion{Start: 4, End: pos, Items: []Item{{Text: "main.go", Display: "main.go"}}}, nil
	}
	e := newEditor(inR, os.Stdout, Options{Complete: slow})
	e.mode, e.buf, e.pos = insertMode, []rune("cat m"), 5

	e.tab()
	inW.Write([]byte("x"))
	start := time.Now()
	k := e.next()
	if k == keyWake { // the "completing…" notice may come first
		e.wake()
		k = e.next()
	}
	if k != 'x' || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("typing waited for the completer: key %q after %v", k, time.Since(start))
	}
	if e.comp.statusLine != "" && e.comp.statusLine != "completing…" {
		t.Fatalf("status %q", e.comp.statusLine)
	}

	e.tab() // a fresh request on the same buffer
	close(release)
	for e.comp.pending {
		if e.next() == keyWake {
			e.wake()
		}
	}
	if got := string(e.buf); got != "cat main.go " {
		t.Errorf("buffer %q", got)
	}

	// A result for a buffer that changed meanwhile is dropped.
	e.buf, e.pos = []rune("cat m"), 5
	e.tab()
	e.insert("z")
	for e.comp.pending {
		if e.next() == keyWake {
			e.wake()
		}
	}
	if got := string(e.buf); got != "cat mz" {
		t.Errorf("stale result applied: %q", got)
	}
}

func TestMenuCycle(t *testing.T) {
	e := &editor{mode: insertMode, buf: []rune("cat ma"), pos: 6}
	e.comp.pending, e.comp.gen = true, 1
	e.comp.reqBuf, e.comp.reqPos = "cat ma", 6
	e.result = &compResult{gen: 1, c: Completion{Start: 4, End: 6, Items: []Item{{Text: "main.go", Display: "main.go"}, {Text: "make.sh", Display: "make.sh"}}}}
	e.comp.pending = true
	e.wake()
	// the first item is selected; the buffer keeps what was typed
	if string(e.buf) != "cat ma" || len(e.comp.menu) != 2 || e.comp.menuIdx != 0 {
		t.Fatalf("menu not shown: %q %d %d", string(e.buf), len(e.comp.menu), e.comp.menuIdx)
	}
	e.tab()
	if string(e.buf) != "cat ma" || e.comp.menuIdx != 1 {
		t.Fatalf("first cycle: %q %d", string(e.buf), e.comp.menuIdx)
	}
	e.tab()
	if e.comp.menuIdx != 0 {
		t.Fatalf("second cycle: %d", e.comp.menuIdx)
	}
	// Enter puts the selection in and closes the menu; it does not run
	if !e.menuEnter('\r') || e.comp.menu != nil || string(e.buf) != "cat main.go " {
		t.Fatalf("enter: menu %v, %q", e.comp.menu, string(e.buf))
	}
	if e.menuEnter('\r') {
		t.Fatal("a second Enter must run the input")
	}
}
