package lineedit

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func filePickEditor() *editor {
	return &editor{
		buf: []rune("print  tail"), pos: len("print "),
		files: testFiles(func(context.Context) ([]Item, error) {
			return []Item{{Text: "'a b'", Display: "a b"}, {Text: "'it''s'", Display: "it's"}}, nil
		}),
	}
}

func awaitFiles(t *testing.T, e *editor) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for e.filePick.loading {
		e.receiveFiles()
		if time.Now().After(deadline) {
			t.Fatal("file source did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestInlineFilesPreviewAndAccept(t *testing.T) {
	e := filePickEditor()
	e.startFilePick()
	awaitFiles(t, e)
	if got := string(e.buf); got != "print 'a b' tail" {
		t.Fatalf("initial preview: %q", got)
	}
	e.filePickKey(keyDown)
	if got := string(e.buf); got != "print 'it''s' tail" || len(e.undo) != 0 {
		t.Fatalf("preview: %q undo %v", got, e.undo)
	}
	e.filePickKey('\r')
	if e.filePick != nil || string(e.buf) != "print 'it''s' tail" || len(e.undo) != 1 {
		t.Fatalf("accept: %q undo %v", e.buf, e.undo)
	}
	e.undoStep(&e.undo, &e.redo)
	if string(e.buf) != "print  tail" || e.pos != len("print ") {
		t.Fatalf("undo: %q @ %d", e.buf, e.pos)
	}
}

func TestInlineFilesCancelAndMultiSelect(t *testing.T) {
	for _, cancelKey := range []rune{27, 3, 7} {
		e := filePickEditor()
		e.startFilePick()
		awaitFiles(t, e)
		e.filePickKey('\t')
		e.filePickKey('\t')
		if string(e.buf) != "print 'a b' 'it''s' tail" {
			t.Fatalf("multiple selections: %q", e.buf)
		}
		e.filePickKey(cancelKey)
		if e.filePick != nil || string(e.buf) != "print  tail" || e.pos != len("print ") || len(e.undo) != 0 {
			t.Fatalf("cancel: %q @ %d", e.buf, e.pos)
		}
	}
}

func TestInlineFilesFilterAndLateResults(t *testing.T) {
	e := filePickEditor()
	gate := make(chan struct{})
	e.files = testFiles(func(ctx context.Context) ([]Item, error) {
		<-gate
		return []Item{{Text: "one", Display: "one"}, {Text: "two", Display: "two"}}, nil
	})
	e.startFilePick()
	for _, k := range "two" {
		e.filePickKey(k)
	}
	close(gate)
	awaitFiles(t, e)
	if string(e.buf) != "print two tail" {
		t.Fatalf("query typed while loading: %q", e.buf)
	}
	e.filePickKey('z')
	if string(e.buf) != "print  tail" {
		t.Fatalf("no match should remove preview: %q", e.buf)
	}
	e.filePickKey('\r')
	if e.filePick == nil {
		t.Fatal("empty selection accepted")
	}
	e.cancelFilePick()
	stopped := make(chan struct{})
	e.files = testFiles(func(ctx context.Context) ([]Item, error) {
		<-ctx.Done()
		close(stopped)
		return []Item{{Text: "stale", Display: "stale"}}, nil
	})
	e.startFilePick()
	e.filePickKey(27)
	<-stopped
	e.receiveFiles()
	if string(e.buf) != "print  tail" || e.filePick != nil {
		t.Fatal("canceled request changed the input")
	}
}

func TestInlineFilesDrawOriginalPrompt(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	e := filePickEditor()
	e.out, e.prompt, e.cont, e.rprompt = f, "PROMPT> ", "CONT> ", "RIGHT"
	e.buf, e.pos = []rune("if $true {\n  print  tail"), len("if $true {\n  print ")
	e.startFilePick()
	awaitFiles(t, e)
	e.refresh()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	s := stripEscapes(string(data))
	if strings.Count(s, "PROMPT> ") != 1 || !strings.Contains(s, "CONT>   print 'a b' tail") || !strings.Contains(s, "RIGHT") || !strings.Contains(s, "pick: ") {
		t.Fatalf("picker must share the real input render: %q", s)
	}
	e.cancelFilePick()
}

func TestInlineFilesSourceError(t *testing.T) {
	e := filePickEditor()
	e.files = testFiles(func(context.Context) ([]Item, error) { return nil, errors.New("fd failed") })
	e.startFilePick()
	awaitFiles(t, e)
	lines, _ := e.filePickLines(80, 10)
	if !strings.Contains(strings.Join(lines, "\n"), "fd failed") || string(e.buf) != "print  tail" {
		t.Fatalf("error: %v, input %q", lines, e.buf)
	}
	e.cancelFilePick()
}

func testFiles(source FileSource) func() FileSource { return func() FileSource { return source } }
