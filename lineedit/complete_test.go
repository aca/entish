package lineedit

import (
	"context"
	"fmt"
	"os"
	"strings"
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

func TestMenuType(t *testing.T) {
	items := []Item{{Text: "checkout"}, {Text: "cherry-pick"}, {Text: "check-ignore"}}
	e := &editor{buf: []rune("git ch"), pos: 6, mode: insertMode}
	e.comp = compState{all: items, menu: items, menuStart: 4, menuEnd: 6, menuBuf: "git ch"}
	texts := func() string {
		var s []string
		for _, it := range e.comp.menu {
			s = append(s, it.Text)
		}
		return strings.Join(s, " ")
	}
	steps := []struct {
		k         rune
		buf, menu string
	}{
		{'p', "git chp", "cherry-pick"},
		{127, "git ch", "checkout cherry-pick check-ignore"},
		{'i', "git chi", "cherry-pick check-ignore"},
		{'z', "git chiz", ""}, // nothing matches: the menu closes
	}
	for _, s := range steps {
		if !e.menuType(s.k) {
			t.Fatalf("%q: not taken", s.k)
		}
		if string(e.buf) != s.buf || texts() != s.menu {
			t.Errorf("%q: got %q [%s], want %q [%s]", s.k, string(e.buf), texts(), s.buf, s.menu)
		}
	}
	if e.menuType('x') {
		t.Error("taken with the menu closed")
	}
}

func TestMenuDesc(t *testing.T) {
	var items []Item
	for i := range 20 {
		items = append(items, Item{Text: fmt.Sprint("opt", i), Display: fmt.Sprint("opt", i), Desc: fmt.Sprint("does ", i)})
	}
	e := &editor{}
	e.comp = compState{all: items, menu: items, menuIdx: 15}
	out := strings.Join(e.belowLines(80), "\n")
	if !strings.Contains(out, "opt15") || !strings.Contains(out, "does 15") || strings.Contains(out, "opt0 ") {
		t.Errorf("selected item not scrolled into view:\n%s", out)
	}
	if !strings.Contains(out, "4 more") {
		t.Errorf("want 4 more after the window:\n%s", out)
	}
}
