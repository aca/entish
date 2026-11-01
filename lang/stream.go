package lang

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Pipelines stream. In `cmd | lines`, cmd runs concurrently and its stdout
// is passed to lines as a *Stream, a str that has not been read yet; lines
// (and lines0) turn it into a *Seq, a list produced one item at a time,
// which map, filter and each pass on. Everywhere else a stream or seq is forced into a plain str or
// list: when it is passed to any other fn, bound to a variable, or is the
// value of a whole pipeline. So variables never hold lazy values.

// Stream is the stdout of a command still running.
type Stream struct {
	r    *os.File
	done chan error

	mu       sync.Mutex
	finished bool
	err      error
	procs    []*os.Process
}

// Seq is a list produced on demand.
type Seq struct {
	next  func() (v Value, ok bool, err error)
	close func()
}

// startStream runs a stage, or a byte pipeline, with its stdout going to a
// pipe the caller reads. Its value is dropped.
func (in *Interp) startStream(stages []Expr, env *Env, io ioCtx, carry Value, has bool) (*Stream, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, raisef(stages[0].pos(), err.Error())
	}
	s := &Stream{r: pr, done: make(chan error, 1)}
	sio := io
	sio.out = pw
	sio.onStart = s.addProc
	go func() {
		var err error
		if len(stages) > 1 {
			_, err = in.bytePipeline(stages, env, sio, carry, has, false)
		} else {
			_, err = in.stage(stages[0], env, sio, carry, has, false)
		}
		pw.Close()
		s.done <- err
	}()
	return s, nil
}

func (s *Stream) addProc(p *os.Process) {
	s.mu.Lock()
	s.procs = append(s.procs, p)
	s.mu.Unlock()
}

// wait waits for the command and returns how it ended.
func (s *Stream) wait() error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return s.err
	}
	s.mu.Unlock()
	err := <-s.done
	s.mu.Lock()
	s.finished, s.err = true, err
	s.mu.Unlock()
	return err
}

// close stops reading early. The command usually ends at once (SIGPIPE or
// EPIPE on its next write); one that doesn't write, e.g. reading a
// terminal, is killed.
func (s *Stream) close() {
	s.r.Close()
	s.mu.Lock()
	finished := s.finished
	s.mu.Unlock()
	if finished {
		return
	}
	select {
	case err := <-s.done:
		s.mu.Lock()
		s.finished, s.err = true, err
		s.mu.Unlock()
	case <-time.After(200 * time.Millisecond):
		s.mu.Lock()
		for _, p := range s.procs {
			p.Kill() // no-op for a process already waited for
		}
		s.mu.Unlock()
		s.wait()
	}
}

// readAll forces the stream: the whole output as a str, like $( ).
func (s *Stream) readAll() (Value, error) {
	b, _ := io.ReadAll(s.r)
	s.r.Close()
	if err := s.wait(); err != nil {
		return nil, err
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

// split turns a stream into a seq of records ended by delim.
func (s *Stream) split(delim byte) *Seq {
	return &Seq{next: records(s.r, delim, s.wait), close: s.close}
}

// records reads records ended by delim from r, one per call; at the end of
// r it reports end(), e.g. how the command writing r ended.
func records(r io.Reader, delim byte, end func() error) func() (Value, bool, error) {
	br := bufio.NewReader(r)
	return func() (Value, bool, error) {
		rec, err := br.ReadString(delim)
		if rec != "" {
			rec = strings.TrimSuffix(rec, string(delim))
			if delim == '\n' {
				rec = strings.TrimSuffix(rec, "\r")
			}
			return rec, true, nil
		}
		if err == nil {
			return "", true, nil
		}
		return nil, false, end()
	}
}

func (q *Seq) collect() (Value, error) {
	l := &List{}
	for {
		v, ok, err := q.next()
		if err != nil {
			q.close()
			return nil, err
		}
		if !ok {
			return l, nil
		}
		l.Items = append(l.Items, v)
	}
}

// force turns a lazy value into a plain one.
func force(v Value) (Value, error) {
	switch v := v.(type) {
	case *Stream:
		return v.readAll()
	case *Seq:
		return v.collect()
	}
	return v, nil
}

func forceAll(args []Value) ([]Value, error) {
	var out []Value
	for i, a := range args {
		switch a.(type) {
		case *Stream, *Seq:
			if out == nil {
				out = append([]Value(nil), args...)
			}
			v, err := force(a)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
	}
	if out == nil {
		return args, nil
	}
	return out, nil
}

// mapSeq and filterSeq stay lazy over a seq.
func (in *Interp) mapSeq(f Value, src *Seq, io ioCtx) *Seq {
	return &Seq{
		next: func() (Value, bool, error) {
			v, ok, err := src.next()
			if !ok || err != nil {
				return nil, false, err
			}
			r, err := in.callValue(f, []Value{v}, io, false, Pos{})
			return r, err == nil, err
		},
		close: src.close,
	}
}

func (in *Interp) filterSeq(f Value, src *Seq, io ioCtx) *Seq {
	return &Seq{
		next: func() (Value, bool, error) {
			for {
				v, ok, err := src.next()
				if !ok || err != nil {
					return nil, false, err
				}
				keep, err := in.callValue(f, []Value{v}, io, false, Pos{})
				if err != nil {
					return nil, false, err
				}
				b, isBool := keep.(bool)
				if !isBool {
					return nil, false, raisef(Pos{}, "filter: the fn must return a bool, got "+typeName(keep))
				}
				if b {
					return v, true, nil
				}
			}
		},
		close: src.close,
	}
}
