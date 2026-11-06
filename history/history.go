// Package history stores accepted inputs in sqlite. Several shells may use
// the same database; each picks up the others' entries at its next prompt.
package history

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS history (
	id   INTEGER PRIMARY KEY,
	cmd  TEXT    NOT NULL,
	cwd  TEXT    NOT NULL DEFAULT '',
	time INTEGER NOT NULL,
	session INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS dirs (
	path TEXT    PRIMARY KEY,
	time INTEGER NOT NULL
);`

type Store struct {
	db      *sql.DB
	cwd     func() string
	session int64 // tags this shell's rows, so other shells' rows don't break its sequence
	t0      int64 // reference time of the weights

	mu      sync.Mutex
	lastID  int64
	lastDir string         // last dir recorded by Visit
	here    string         // dir of the last Visit, the context of Suggest
	latest  map[string]use // cmd -> its newest use
	entries []string       // unique, oldest first; rebuilt when dirty
	dirty   bool

	// Uses weighted by recency, for Suggest.
	freq  map[string]float64
	inDir map[[2]string]float64 // {cwd, cmd}
	after map[[2]string]float64 // {previous cmd of the session, cmd}
	tail  map[int64]string      // session -> its last cmd
}

// halfLife is the age in seconds at which a use counts half.
const halfLife = 7 * 24 * 3600

type use struct {
	id   int64
	time int64
	cwd  string
}

// DefaultPath is $XDG_DATA_HOME/entish/history.db, or ~/.local/share/entish/history.db.
func DefaultPath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "entish", "history.db")
}

// Open opens or creates the database. cwd is recorded with each entry.
func Open(path string, cwd func() string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=2000", schema} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Databases from before sessions were recorded lack the column.
	if _, err := db.Exec("SELECT session FROM history LIMIT 0"); err != nil {
		if _, err := db.Exec("ALTER TABLE history ADD COLUMN session INTEGER NOT NULL DEFAULT 0"); err != nil {
			db.Close()
			return nil, err
		}
	}
	now := time.Now()
	s := &Store{
		db: db, cwd: cwd, session: now.UnixNano(), t0: now.Unix(),
		latest: map[string]use{}, freq: map[string]float64{},
		inDir: map[[2]string]float64{}, after: map[[2]string]float64{}, tail: map[int64]string{},
	}
	if err := s.seedDirs(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.load(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// load reads rows added since the last load (by this or another shell).
// The caller holds s.mu, or s is not shared yet.
func (s *Store) load() error {
	rows, err := s.db.Query("SELECT id, cmd, cwd, time, session FROM history WHERE id > ? ORDER BY id", s.lastID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u use
		var cmd string
		var session int64
		if err := rows.Scan(&u.id, &cmd, &u.cwd, &u.time, &session); err != nil {
			return err
		}
		s.latest[cmd], s.lastID, s.dirty = u, u.id, true
		w := math.Exp2(float64(u.time-s.t0) / halfLife)
		s.freq[cmd] += w
		s.inDir[[2]string{u.cwd, cmd}] += w
		s.after[[2]string{s.tail[session], cmd}] += w
		s.tail[session] = cmd
	}
	return rows.Err()
}

func (s *Store) Add(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	cwd := ""
	if s.cwd != nil {
		cwd = s.cwd()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("INSERT INTO history (cmd, cwd, time, session) VALUES (?, ?, ?, ?)", cmd, cwd, time.Now().Unix(), s.session); err != nil {
		return err
	}
	return s.load()
}

// Entries returns unique inputs, oldest first, including other shells'
// entries added since the last call.
func (s *Store) Entries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return append([]string(nil), s.sorted()...)
}

func (s *Store) sorted() []string {
	if s.dirty {
		s.entries = s.entries[:0]
		for cmd := range s.latest {
			s.entries = append(s.entries, cmd)
		}
		sort.Slice(s.entries, func(i, j int) bool { return s.latest[s.entries[i]].id < s.latest[s.entries[j]].id })
		s.dirty = false
	}
	return s.entries
}

// Last returns when and in which directory cmd was last run.
func (s *Store) Last(cmd string) (time.Time, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.latest[cmd]
	if !ok {
		return time.Time{}, "", false
	}
	return time.Unix(u.time, 0), u.cwd, true
}

// seedDirs fills an empty dirs table from the dirs commands ran in.
func (s *Store) seedDirs() error {
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM dirs").Scan(&n); err != nil || n > 0 {
		return err
	}
	_, err := s.db.Exec("INSERT INTO dirs (path, time) SELECT cwd, max(time) FROM history WHERE cwd != '' GROUP BY cwd")
	return err
}

// Visit records that the shell is in dir, for Dirs.
func (s *Store) Visit(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.here = dir
	if dir == s.lastDir {
		return nil
	}
	_, err := s.db.Exec("INSERT INTO dirs (path, time) VALUES (?, ?) ON CONFLICT (path) DO UPDATE SET time = excluded.time", dir, time.Now().Unix())
	if err == nil {
		s.lastDir = dir
	}
	return err
}

// Dir is a visited directory.
type Dir struct {
	Path string
	Time time.Time // last visit
}

// Dirs returns the visited directories that still exist, most recent
// first, without the current one.
func (s *Store) Dirs() []Dir {
	cwd := ""
	if s.cwd != nil {
		cwd = s.cwd()
	}
	rows, err := s.db.Query("SELECT path, time FROM dirs ORDER BY time DESC, path")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Dir
	for rows.Next() {
		var path string
		var t int64
		if rows.Scan(&path, &t) != nil {
			return out
		}
		if fi, err := os.Stat(path); path != cwd && err == nil && fi.IsDir() {
			out = append(out, Dir{path, time.Unix(t, 0)})
		}
	}
	return out
}

// Suggest returns the entry extending buf that is most likely next, for
// the editor's gray hint. Among the matches, it mixes each one's share of
// all uses, of the uses in the current dir and of the uses right after
// this shell's previous command; uses fade with age. Ties go to the newer
// entry. It only reads memory, so it is fast; the editor still runs it
// asynchronously like any Suggester.
func (s *Store) Suggest(ctx context.Context, buf string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type match struct {
		cmd                string
		freq, inDir, after float64
	}
	var ms []match
	var sum match
	prev := s.tail[s.session]
	for i, e := range s.sorted() {
		if i%1024 == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if len(e) > len(buf) && strings.HasPrefix(e, buf) {
			m := match{e, s.freq[e], s.inDir[[2]string{s.here, e}], s.after[[2]string{prev, e}]}
			ms = append(ms, m)
			sum.freq, sum.inDir, sum.after = sum.freq+m.freq, sum.inDir+m.inDir, sum.after+m.after
		}
	}
	share := func(x, total float64) float64 {
		if total == 0 {
			return 0
		}
		return x / total
	}
	best, bestScore := "", -1.0
	for _, m := range ms { // oldest first, so >= lets the newer win ties
		if sc := 0.2*share(m.freq, sum.freq) + 0.3*share(m.inDir, sum.inDir) + 0.5*share(m.after, sum.after); sc >= bestScore {
			best, bestScore = m.cmd, sc
		}
	}
	return best, nil
}
