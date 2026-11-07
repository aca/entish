package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5200 * time.Millisecond: "5.2s",
		62 * time.Second:        "1m02s",
		3723 * time.Second:      "1h02m03s",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestRightPrompt(t *testing.T) {
	now := time.Date(2026, 10, 30, 22, 4, 5, 0, time.UTC)
	plain := func(s string) string { return stripANSI(s) }
	if got := plain(rightPrompt(0, false, now)); got != "10/30/2026 10:04:05 PM" {
		t.Errorf("%q", got)
	}
	if got := plain(rightPrompt(2, true, now)); got != "[direnv] 2 10/30/2026 10:04:05 PM" {
		t.Errorf("%q", got)
	}
}

func stripANSI(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

func TestTitle(t *testing.T) {
	var b strings.Builder
	tm := newTermMarks(&b, func() string { return "/tmp" })
	tm.commandStart("echo \x1b]0;evil\x07 hi\nmore")
	want := "\x1b]2;echo ]0;evil hi …\x1b\\" + osc133("C")
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}
