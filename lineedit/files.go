package lineedit

import (
	"context"
	"strings"
)

type filePickResult struct {
	items []Item
	err   error
}

type filePickState struct {
	saved   []rune
	pos     int
	picker  *picker
	items   []Item
	result  chan filePickResult
	cancel  context.CancelFunc
	loading bool
	err     error
}

func (e *editor) startFilePick() {
	if e.files == nil {
		return
	}
	e.cancelCompletion()
	e.clearSuggestion()
	ctx, cancel := context.WithCancel(context.Background())
	s := &filePickState{
		saved: append([]rune(nil), e.buf...), pos: e.pos,
		picker: newPicker(nil), result: make(chan filePickResult, 1),
		cancel: cancel, loading: true,
	}
	e.filePick = s
	source := e.files()
	go func() {
		items, err := source(ctx)
		s.result <- filePickResult{items, err}
		e.wakeUp()
	}()
}

func (e *editor) receiveFiles() {
	s := e.filePick
	if s == nil || !s.loading {
		return
	}
	select {
	case r := <-s.result:
		s.items, s.err, s.loading = r.items, r.err, false
		if r.err == nil {
			for _, item := range s.items {
				s.picker.items = append(s.picker.items, item.Display)
			}
			s.picker.filter()
			e.previewFiles()
		}
	default:
	}
}

func (s *filePickState) text() string {
	var parts []string
	for _, i := range s.picker.selection() {
		parts = append(parts, s.items[i].Text)
	}
	return strings.Join(parts, " ")
}

func (e *editor) previewFiles() {
	s := e.filePick
	e.buf, e.pos = append([]rune(nil), s.saved...), s.pos
	e.insert(s.text())
}

func (e *editor) cancelFilePick() {
	if s := e.filePick; s != nil {
		s.cancel()
		e.buf, e.pos = s.saved, s.pos
		e.filePick = nil
	}
}

func (e *editor) filePickKey(k rune) {
	s := e.filePick
	if done, _ := s.picker.key(k, e.paste); done {
		text, accept := s.text(), s.picker.done
		e.cancelFilePick()
		if accept && text != "" {
			// Preview never touches undo history; acceptance is one edit.
			e.insSnap = false
			e.applyKeyResult(text, false)
		}
		return
	}
	e.previewFiles()
}

func (e *editor) filePickLines(cols, rows int) ([]string, int) {
	s := e.filePick
	lines, col := s.picker.lines(cols, min(searchRows, rows))
	status := ""
	switch {
	case s.loading:
		status = "finding files… (Esc to cancel)"
	case s.err != nil:
		status = s.err.Error() + " (Esc to cancel)"
	case len(s.picker.matches) == 0:
		status = "no matches"
	}
	if status != "" && rows > 0 {
		lines = append(lines, "\x1b[2m"+truncate(displayText([]rune(status)), max(1, cols-1))+"\x1b[0m")
	}
	return lines, col
}
