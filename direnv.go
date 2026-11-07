package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/aca/entish/lang"
	"github.com/aca/entish/lineedit"
)

// direnvHook runs `direnv export json` before each prompt, in the
// background, so the prompt and typing never wait for it (a nix flake .envrc
// can take seconds). When it finishes, the right prompt updates at once;
// its env changes are applied right before the next command runs, waiting
// for direnv only if it is still running then. direnv's own messages are
// held back until then, so they don't garble the line being edited.
type direnvHook struct {
	in      *lang.Interp
	ed      lineedit.Editor
	rprompt func(active bool) string

	mu      sync.Mutex
	running bool
	done    chan struct{} // closed when the current run finishes
	ready   bool          // a finished run not applied yet
	changes map[string]*string
	stderr  []byte
	err     error
}

// direnvWait is how long a command waits for a direnv still running.
const direnvWait = 5 * time.Second

// beforePrompt applies a finished run and starts a new one.
func (d *direnvHook) beforePrompt() {
	d.apply()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return // a slow run from the last prompt is still going
	}
	path := findExecutable("direnv", d.in.Getenv("PATH"))
	if path == "" {
		return
	}
	env, cwd := d.in.Environ(), d.in.Cwd()
	done := make(chan struct{})
	d.running, d.done = true, done
	go func() {
		var out, errb bytes.Buffer
		cmd := exec.Command(path, "export", "json")
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = cwd, env, &out, &errb
		err := cmd.Run()
		var changes map[string]*string
		if err == nil && out.Len() > 0 {
			err = json.Unmarshal(out.Bytes(), &changes)
		}
		d.mu.Lock()
		d.running, d.ready = false, true
		d.changes, d.stderr, d.err = changes, errb.Bytes(), err
		d.mu.Unlock()
		close(done)
		if len(changes) > 0 {
			d.ed.SetRightPrompt(d.rprompt(direnvActive(env, changes)))
		}
	}()
}

// beforeCommand makes sure the command runs with direnv's changes.
func (d *direnvHook) beforeCommand() {
	d.mu.Lock()
	running, done := d.running, d.done
	d.mu.Unlock()
	if running {
		select {
		case <-done:
		case <-time.After(direnvWait):
			fmt.Fprintln(os.Stderr, "\x1b[2mdirenv: still running; this command runs without its changes\x1b[0m")
		}
	}
	d.apply()
}

func (d *direnvHook) apply() {
	d.mu.Lock()
	if !d.ready {
		d.mu.Unlock()
		return
	}
	changes, stderr, err := d.changes, d.stderr, d.err
	d.ready, d.changes, d.stderr, d.err = false, nil, nil, nil
	d.mu.Unlock()
	if len(stderr) > 0 {
		os.Stderr.Write(stderr)
	} else if err != nil {
		fmt.Fprintln(os.Stderr, "direnv:", err)
	}
	if len(changes) > 0 {
		d.in.ApplyEnv(changes)
	}
}

// direnvActive reports whether an .envrc is loaded after changes:
// direnv sets DIRENV_DIR while one is.
func direnvActive(env []string, changes map[string]*string) bool {
	if v, ok := changes["DIRENV_DIR"]; ok {
		return v != nil && *v != ""
	}
	for _, kv := range env {
		if len(kv) > len("DIRENV_DIR=") && kv[:len("DIRENV_DIR=")] == "DIRENV_DIR=" {
			return true
		}
	}
	return false
}

func findExecutable(name, path string) string {
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
