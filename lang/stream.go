package lang

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"sync"
)

// Every call emits an output stream (bytes) and a value stream. Values flow
// between the stages of a pipeline through a vpipe, as bytes flow through
// an os pipe: in `a |> b`, a sends each value as it emits it and b reads
// them as they come.

// errVClosed is what a send gets once the reader stopped, like EPIPE.
var errVClosed = errors.New("value pipe closed")

type vpipe struct {
	ch   chan Value
	done chan struct{}
	once sync.Once
}

// newVpipe buffers a few values, as an os pipe buffers bytes: the writer
// runs ahead of the reader by at most that many.
func newVpipe() *vpipe { return &vpipe{ch: make(chan Value, 64), done: make(chan struct{})} }

// send emits v to the reader. It fails once the reader stopped.
func (p *vpipe) send(v Value) error {
	select {
	case p.ch <- v:
		return nil
	case <-p.done:
		return errVClosed
	}
}

// closeW ends the stream after the values sent.
func (p *vpipe) closeW() { close(p.ch) }

// next is the next value; ok is false at the end. A nil vpipe is an empty
// stream: the value input of a stage that no |> feeds.
func (p *vpipe) next() (Value, bool) {
	if p == nil {
		return nil, false
	}
	select {
	case v, ok := <-p.ch:
		return v, ok
	case <-p.done:
		return nil, false
	}
}

// closeR stops reading; the writer's next send fails.
func (p *vpipe) closeR() { p.once.Do(func() { close(p.done) }) }

// lostReader: err only says the next stage stopped reading.
func lostReader(err error) bool {
	return errors.Is(err, errVClosed) || isEPIPE(err)
}

// records reads records ended by delim from r and emits each.
func records(r io.Reader, delim byte, emit func(Value) error) error {
	br := bufio.NewReader(r)
	for {
		rec, err := br.ReadString(delim)
		if rec != "" {
			rec = strings.TrimSuffix(rec, string(delim))
			if delim == '\n' {
				rec = strings.TrimSuffix(rec, "\r")
			}
			if e := emit(rec); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
