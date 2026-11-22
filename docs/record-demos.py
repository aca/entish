#!/usr/bin/env python3
"""Capture the real shell, including its ANSI output and cursor, for GitHub Pages.

Regenerate from the repository root:
    uv run --with pyte==0.8.2 python docs/record-demos.py

Requires Go, Git, Fish, and a Unix PTY. pyte decodes the recorded terminal output;
no search results, completion menus, or prompts are drawn by this script.
All commands run in a temporary home, config, data directory, and Git repository.
The .cast files preserve the original output and timing for independent replay.
"""
import codecs
import fcntl
import html
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import sqlite3
import struct
import subprocess
import tempfile
import termios
import time

import pyte

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'docs' / 'recordings'
ROWS = 12


class Terminal:
    def __init__(self, binary, cwd, env, columns):
        self.columns = columns
        self.screen = pyte.Screen(columns, ROWS)
        self.stream = pyte.Stream(self.screen)
        self.decoder = codecs.getincrementaldecoder('utf-8')()
        self.events = []
        self.frames = []
        self.started = time.monotonic()
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack('HHHH', ROWS, columns, 0, 0))
            os.chdir(cwd)
            os.execve(binary, [str(binary)], env)
        self.read(0.7)

    def snapshot(self):
        lines = []
        for y in range(ROWS):
            runs = []
            for x in range(self.columns):
                cell = self.screen.buffer[y][x]
                classes = []
                if cell.fg != 'default':
                    classes.append('fg-' + cell.fg)
                if cell.reverse:
                    classes.append('reverse')
                if cell.bold:
                    classes.append('bold')
                if not self.screen.cursor.hidden and (x, y) == (self.screen.cursor.x, self.screen.cursor.y):
                    classes.append('cursor')
                style = ' '.join(classes)
                if runs and runs[-1][0] == style:
                    runs[-1][1] += cell.data
                else:
                    runs.append([style, cell.data])
            # Discard only unstyled blank cells at the right edge.
            if runs and not runs[-1][0]:
                runs[-1][1] = runs[-1][1].rstrip()
                if not runs[-1][1]:
                    runs.pop()
            lines.append(runs)
        while lines and not lines[-1]:
            lines.pop()
        return lines

    def read(self, seconds):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            if select.select([self.fd], [], [], min(1 / 30, max(0, end - time.monotonic())))[0]:
                try:
                    data = os.read(self.fd, 65536)
                except OSError as error:
                    raise RuntimeError('entish exited during recording') from error
                if not data:
                    raise RuntimeError('entish closed the terminal')
                text = self.decoder.decode(data)
                self.events.append([round(time.monotonic() - self.started, 4), 'o', text])
                self.stream.feed(text)
            frame = self.snapshot()
            if not self.frames or self.frames[-1][1] != frame:
                self.frames.append([round(time.monotonic() - self.started, 4), frame])

    def key(self, keys, delay=0.4):
        os.write(self.fd, keys.encode())
        self.read(delay)

    def type(self, text):
        for char in text:
            self.key(char, 0.18)

    def close(self):
        os.killpg(self.pid, signal.SIGTERM)
        os.waitpid(self.pid, 0)
        os.close(self.fd)

    def save(self, name):
        elapsed = time.monotonic() - self.started
        header = {'version': 2, 'width': self.columns, 'height': ROWS,
                  'title': 'entish ' + name, 'env': {'TERM': 'xterm-256color'}}
        with (OUT / f'{name}.cast').open('w') as file:
            for event in [header, *self.events]:
                file.write(json.dumps(event, ensure_ascii=False) + '\n')
        frames = []
        # Skip startup before the first prompt; all remaining cells are captured output.
        start = next(i for i, (_, lines) in enumerate(self.frames)
                     if '> ' in ''.join(run[1] for line in lines for run in line))
        for i in range(start, len(self.frames)):
            timestamp, lines = self.frames[i]
            until = self.frames[i + 1][0] if i + 1 < len(self.frames) else elapsed
            frames.append({'ms': max(16, round((until - timestamp) * 1000)), 'lines': lines})
        return {'columns': self.columns, 'rows': max(len(f['lines']) for f in frames), 'frames': frames}


def run():
    OUT.mkdir(exist_ok=True)
    recordings = {name: {} for name in ('history', 'location', 'completion')}
    with tempfile.TemporaryDirectory(prefix='entish-record-') as temporary:
        base = Path(temporary)
        binary = base / 'entish'
        subprocess.run(['go', 'build', '-o', str(binary), '.'], cwd=ROOT, check=True)
        home = base / 'home'
        repo = home / 'src' / 'entish'
        for directory in (repo / 'docs', home / 'src' / 'website', home / 'Downloads', home / 'notes'):
            directory.mkdir(parents=True)
        for name in ('design.md', 'guide.md', 'intro.md'):
            (repo / 'docs' / name).write_text('entish documentation\n')
        subprocess.run(['git', 'init', '-q', str(repo)], check=True)
        subprocess.run(['git', '-C', str(repo), 'add', '.'], check=True)
        subprocess.run(['git', '-C', str(repo), '-c', 'user.name=Demo', '-c', 'user.email=demo@example.com',
                        '-c', 'commit.gpgsign=false', 'commit', '-qm', 'Add documentation'], check=True)
        # Use a small real PATH to exclude personal shell integrations and completions.
        bin_dir = base / 'bin'
        bin_dir.mkdir()
        for command in ('git', 'fish', 'cat', 'ls', 'pwd'):
            executable = shutil.which(command)
            if executable is None:
                raise RuntimeError(f'{command} must be installed to record the demos')
            (bin_dir / command).symlink_to(executable)
        env = {'HOME': str(home), 'XDG_CONFIG_HOME': str(base / 'config'),
               'XDG_DATA_HOME': str(base / 'data'), 'PATH': str(bin_dir),
               'TERM': 'xterm-256color', 'LANG': 'C.UTF-8'}
        seed = Terminal(binary, repo, env, 68)
        try:
            for directory in ('notes', 'src/website', 'Downloads', 'src/entish'):
                seed.key(f"cd '{home / directory}'\r", 1.1)
            for command in ('git status', 'git log --oneline', 'git diff', 'pwd'):
                seed.key(command + '\r', 0.5)
        finally:
            seed.close()
        # Omit setup commands from the demo history, retaining the real visits.
        with sqlite3.connect(base / 'data' / 'entish' / 'history.db') as database:
            database.execute("DELETE FROM history WHERE cmd LIKE 'cd %'")
        for variant, columns in (('wide', 68), ('narrow', 38)):
            for name in recordings:
                terminal = Terminal(binary, home if name == 'location' else repo, env, columns)
                try:
                    if name == 'history':
                        terminal.key('\x12', 0.9)  # Ctrl-R
                        terminal.type('git !log')
                        terminal.read(1.0)
                        terminal.key('\x1b[B', 0.9)  # Down
                        terminal.key('\r', 2.4)
                    elif name == 'location':
                        terminal.key('\x04', 0.9)  # Ctrl-D
                        terminal.type('src')
                        terminal.read(1.0)
                        terminal.key('\x1b[B', 0.9)
                        terminal.key('\x1b[A', 0.9)
                        terminal.key('\r', 2.4)
                    else:
                        terminal.type('git checkout --t')
                        terminal.key('\t', 1.2)  # Fish supplies options and descriptions.
                        terminal.key('\t', 0.9)
                        terminal.key('\r', 2.4)  # Enter puts the selection in without running.
                    recording = terminal.save(f'{name}-{variant}')
                    screens = ['\n'.join(''.join(run[1] for run in line) for line in frame['lines'])
                               for frame in recording['frames']]
                    poster_text = {'history': 'history: git !log', 'location': 'location: src',
                                   'completion': '--theirs  (Keep staged changes)'}[name]
                    recording['poster'] = next(i for i, text in enumerate(screens) if poster_text in text)
                    expected = {'history': '~/src/entish> git status', 'location': '~/src/entish>',
                                'completion': '~/src/entish> git checkout --track'}[name]
                    assert expected in screens[-1], f'{name}: unexpected final screen {screens[-1]!r}'
                    recordings[name][variant] = recording
                    print(f'{name}-{variant}: {len(recording["frames"])} captured frames')
                finally:
                    terminal.close()
    (OUT / 'frames.js').write_text('// Generated by docs/record-demos.py from real PTY output.\n'
                                  'window.entishRecordings = ' + json.dumps(recordings, ensure_ascii=False,
                                                                          separators=(',', ':')) + ';\n')
    # Keep the no-JavaScript fallback sourced from the same capture.
    import re
    page = ROOT / 'docs' / 'index.html'
    content = page.read_text()
    for name, variants in recordings.items():
        recording = variants['wide']
        lines = recording['frames'][recording['poster']]['lines']
        text = '\n'.join(''.join(run[1] for run in line).rstrip() for line in lines)
        pattern = rf'(<figure[^>]*data-demo="{name}"[^>]*>\s*<pre[^>]*>).*?(</pre>)'
        content, count = re.subn(pattern, lambda m: m[1] + html.escape(text) + m[2], content, flags=re.S)
        assert count == 1, f'missing fallback for {name}'
    page.write_text(content)


if __name__ == '__main__':
    run()
