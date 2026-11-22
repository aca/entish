package lineedit

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func navFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"a dir/child", "b dir", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"file.txt": "hello\nworld", "binary": "a\x00b", "bad\x1b[31m": "\x1b]0;title\a\n한글"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestNavigatorMovesAndCancellation(t *testing.T) {
	dir := navFixture(t)
	cwd, _ := os.Getwd()
	n, err := newNavigator(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n.selected() != "a dir" || n.preview[0] != "child/" {
		t.Fatalf("selection %q, preview %q", n.selected(), n.preview)
	}
	n.key(keyDown)
	n.key(keyRight)
	if n.path != filepath.Join(dir, "b dir") || len(n.matches) != 0 {
		t.Fatalf("empty directory: %q, %v", n.path, n.matches)
	}
	n.key(keyDown) // empty directory must remain navigable
	n.key(keyLeft)
	if n.path != dir || n.selected() != "b dir" {
		t.Fatalf("parent selection: %q, %q", n.path, n.selected())
	}
	n.key('k')
	n.key('l')
	if done, accept, intr := n.key('\r'); !done || !accept || intr {
		t.Fatalf("accept: %v %v %v", done, accept, intr)
	}
	for _, key := range []rune{'q', 27, 3} {
		if done, accept, intr := n.key(key); !done || accept || intr != (key == 3) {
			t.Errorf("cancel %d: %v %v %v", key, done, accept, intr)
		}
	}
	if got, _ := os.Getwd(); got != cwd {
		t.Errorf("navigator changed process cwd: %q", got)
	}
	n, err = newNavigator(filepath.Join(dir, "b dir"))
	if err != nil {
		t.Fatal(err)
	}
	n.key('h')
	if n.selected() != "b dir" {
		t.Fatalf("ascending from initial directory selected %q", n.selected())
	}
}

func TestNavigatorFilterHiddenAndSymlinks(t *testing.T) {
	dir := navFixture(t)
	if err := os.Symlink(filepath.Join(dir, "a dir"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	n, err := newNavigator(dir)
	if err != nil {
		t.Fatal(err)
	}
	n.key('.')
	if n.entries[0].name != ".hidden" {
		t.Fatalf("hidden directory missing: %v", n.entries)
	}
	n.key('/')
	for _, key := range "linked" {
		n.key(key)
	}
	if len(n.matches) != 1 || !n.matches[0].dir || !n.matches[0].link {
		t.Fatalf("symlink filter: %v", n.matches)
	}
	if done, _, _ := n.key('\r'); done || n.filtering {
		t.Fatal("Enter should finish filtering without exiting")
	}
	n.key('l')
	if n.path != filepath.Join(dir, "linked") || n.selected() != "child" {
		t.Fatalf("symlink navigation: %q %q", n.path, n.selected())
	}
	n.key('h')
	n.key('/')
	for _, key := range "no match" {
		n.key(key)
	}
	if len(n.matches) != 0 {
		t.Fatal("unexpected match")
	}
	n.key(27)
	if len(n.matches) == 0 || n.filtering || len(n.query) != 0 {
		t.Fatal("Esc should clear filter")
	}
}

func TestNavigatorErrorsAndPreview(t *testing.T) {
	dir := navFixture(t)
	n, err := newNavigator(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newNavigator(filepath.Join(dir, "file.txt")); err == nil {
		t.Fatal("file accepted as directory")
	}
	if err := os.Remove(filepath.Join(dir, "b dir")); err != nil {
		t.Fatal(err)
	}
	n.key('j')
	n.key('l')
	if n.path != dir || n.message == "" {
		t.Fatal("failed move must keep path and show error")
	}
	if got := navPreview(filepath.Join(dir, "file.txt")); strings.Join(got, "\n") != "hello\nworld" {
		t.Fatalf("text preview: %q", got)
	}
	if got := navPreview(filepath.Join(dir, "binary")); !strings.Contains(got[0], "binary file") {
		t.Fatalf("binary preview: %q", got)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if got := navPreview(fifo); !strings.Contains(got[0], "special file") {
		t.Fatalf("FIFO preview: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "long"), []byte(strings.Repeat("x", 100000)), 0600); err != nil {
		t.Fatal(err)
	}
	if got := navPreview(filepath.Join(dir, "long")); len(got[0]) > 16*1024 {
		t.Fatalf("unbounded preview: %d", len(got[0]))
	}
}

func TestNavigatorDraw(t *testing.T) {
	n, err := newNavigator(navFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	n.preview = []string{"\x1b]0;title\a", "한글"}
	n.message = strings.Repeat("界", 100)
	for _, size := range [][2]int{{100, 24}, {40, 8}, {1, 1}, {60, 4}, {80, 2}} {
		cols, rows := size[0], size[1]
		got := strings.Join(n.draw(cols, rows), "\r\n")
		if strings.Contains(got, "\x1b]0;") || strings.Contains(got, "\x1b[31m") {
			t.Fatal("terminal controls in filename or preview")
		}
		lines := strings.Split(stripEscapes(got), "\r\n")
		if len(lines) > rows {
			t.Errorf("%v: %d rows", size, len(lines))
		}
		for _, line := range lines {
			if w := runesWidth([]rune(line)); w > max(1, cols-1) {
				t.Errorf("%v: width %d: %q", size, w, line)
			}
		}
	}
}

type navDirs struct{ cwd, cd string }

func (d *navDirs) Dirs() []Dir   { return nil }
func (d *navDirs) Cwd() string   { return d.cwd }
func (d *navDirs) Cd(dir string) { d.cd = dir }
func (d *navDirs) Back()         {}
func (d *navDirs) Forward()      {}

// TestNavMode checks that Ctrl+N browses below the input and keeps it.
func TestNavMode(t *testing.T) {
	dir := navFixture(t)
	d := &navDirs{cwd: dir}
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	e := &editor{in: out, out: out, dirs: d, buf: []rune("echo hi"), pos: 7, restore: func() {}}
	e.insertKey(14)
	if e.nav == nil || e.nav.path != dir {
		t.Fatal("Ctrl+N did not open the navigator")
	}
	e.refresh()
	b, _ := os.ReadFile(out.Name())
	if s := stripEscapes(string(b)); !strings.Contains(s, "echo hi\r\nnav: "+dir) {
		t.Errorf("navigator not drawn below the input: %q", s)
	}
	for _, k := range []rune{'l', 'q'} { // into "a dir", then cancel
		e.navKey(k)
	}
	if e.nav != nil || d.cd != "" || string(e.buf) != "echo hi" {
		t.Errorf("cancel: nav %v, cd %q, buf %q", e.nav != nil, d.cd, string(e.buf))
	}
	e.insertKey(14)
	for _, k := range []rune{'l', '\r'} {
		e.navKey(k)
	}
	if e.nav != nil || d.cd != filepath.Join(dir, "a dir") || string(e.buf) != "echo hi" {
		t.Errorf("accept: nav %v, cd %q, buf %q", e.nav != nil, d.cd, string(e.buf))
	}
}
