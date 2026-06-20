package ingest

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mattn/go-isatty"
)

// Progress renders per-file build events. In a TTY the last event is
// rewritten on a single line using CR + ANSI clear-to-eol, so a long
// build produces one visible status line that updates in place. In a
// non-TTY (piped, redirected, CI) each event is emitted as its own
// line, matching the historical behavior.
type Progress struct {
	Ctx   context.Context
	Mu    sync.Mutex
	Out   io.Writer
	Quiet bool
	Tty   bool
}

func NewProgress(ctx context.Context, w io.Writer, quiet bool) *Progress {
	if quiet {
		return &Progress{Ctx: ctx, Quiet: true}
	}
	t, ok := w.(*os.File)
	return &Progress{Ctx: ctx, Out: w, Quiet: false, Tty: ok && isatty.IsTerminal(t.Fd())}
}

func (p *Progress) Event(kind, path string) {
	if p.Quiet {
		return
	}
	if p.Ctx != nil {
		if err := p.Ctx.Err(); err != nil {
			return
		}
	}
	p.Mu.Lock()
	defer p.Mu.Unlock()
	if p.Tty {
		fmt.Fprintf(p.Out, "\r%-7s %s\033[K", kind, path)
		return
	}
	fmt.Fprintf(p.Out, "%s %s\n", kind, path)
}

func (p *Progress) Done() {
	if p.Quiet || !p.Tty {
		return
	}
	if p.Ctx != nil {
		if err := p.Ctx.Err(); err != nil {
			return
		}
	}
	p.Mu.Lock()
	defer p.Mu.Unlock()
	fmt.Fprintln(p.Out)
}
