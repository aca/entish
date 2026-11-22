package complete

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/aca/entish/lineedit"
)

// Args completes the last of words, the words of a command up to the
// cursor. Words are unquoted and the last one may be partial; the returned
// Texts are unquoted too, Shell quotes them as entish words.
type Args func(ctx context.Context, words []string) ([]lineedit.Item, error)

// Fish completes arguments with fish's `complete -C`, run in the shell's
// cwd and env, so commands get the completions fish has for them.
func Fish(cwd func() string, environ func() []string) Args {
	return func(ctx context.Context, words []string) ([]lineedit.Item, error) {
		q := make([]string, len(words))
		for i, w := range words {
			q[i] = fishQuote(w)
		}
		cmd := exec.CommandContext(ctx, "fish", "-c", "complete -C -- $argv[1]", strings.Join(q, " "))
		cmd.Dir, cmd.Env = cwd(), environ()
		// fish may start helpers (git for branch names): give them one
		// process group and kill all of it when ctx is canceled.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && len(ee.Stderr) > 0 {
				line, _, _ := bytes.Cut(bytes.TrimSpace(ee.Stderr), []byte("\n"))
				return nil, fmt.Errorf("fish: %s", line)
			}
			return nil, err
		}
		var items []lineedit.Item
		for _, line := range strings.Split(string(out), "\n") {
			if line == "" {
				continue
			}
			text, desc, _ := strings.Cut(line, "\t")
			items = append(items, lineedit.Item{Text: text, Display: text, Desc: desc,
				NoSpace: strings.HasSuffix(text, "/") || strings.HasSuffix(text, "=")})
		}
		return items, nil
	}
}

// fishQuote returns s as a literal fish word.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
