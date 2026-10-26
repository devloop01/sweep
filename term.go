//go:build darwin && arm64

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/devloop01/sweep/internal/sweep"
)

// watch draws a one-line status on stderr while a scan runs, if stderr is a
// terminal: how many folders were read and which one is being read. The
// returned func stops it and erases the line.
func watch(e env, p *sweep.Progress, root string) (stop func()) {
	if !e.live {
		return func() {}
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				fmt.Fprint(e.stderr, "\r\x1b[K")
				return
			case <-tick.C:
				rel, _ := filepath.Rel(root, p.Current())
				fmt.Fprint(e.stderr, "\r\x1b[K"+status(p.Dirs(), rel, termWidth()))
			}
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}
}

// status is the scan status line, cut to fit width columns so it never
// wraps (which would break redrawing it in place).
func status(dirs int64, current string, width int) string {
	s := fmt.Sprintf("Scanning… %d folders  ", dirs)
	room := width - utf8.RuneCountInString(s) - 1
	path := []rune(printable(current))
	switch {
	case room < 8:
		return s[:min(len(s), max(width-1, 0))]
	case len(path) > room:
		// ponytail: counts runes, not columns; wide (CJK) names can still wrap.
		path = append([]rune("…"), path[len(path)-room+1:]...)
	}
	return s + string(path)
}

// termWidth is stderr's terminal width in columns, or 80.
func termWidth() int {
	var ws struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stderr.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.cols == 0 {
		return 80
	}
	return int(ws.cols)
}

const (
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
)

// paint wraps s in an ANSI style when color output is on.
func (e env) paint(style, s string) string {
	if !e.color || style == "" {
		return s
	}
	return style + s + "\x1b[0m"
}

// sizeStyle colors a size so the big folders stand out.
func sizeStyle(b int64) string {
	switch {
	case b >= 1<<30:
		return red + bold
	case b >= 100<<20:
		return yellow
	case b < 1<<20:
		return dim
	}
	return ""
}

// colorOK reports whether to use colors on a stream: only on a terminal,
// and never when NO_COLOR is set (https://no-color.org) or TERM is dumb.
func colorOK(isTerminal bool, getenv func(string) string) bool {
	return isTerminal && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
