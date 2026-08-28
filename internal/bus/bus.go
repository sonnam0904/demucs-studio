// Package bus pushes log lines and progress updates from the Go backend to the
// webview. It is the single place that knows about Wails' event names.
package bus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Event names, mirrored in frontend/src/api.ts.
const (
	EventLog      = "app:log"
	EventProgress = "app:progress"
	EventBusy     = "app:busy"
)

// Log levels understood by the frontend.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelDebug = "debug"
)

// LogLine is one entry in the activity log pane.
type LogLine struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Progress describes the state of the current long-running phase.
// Percent is -1 when the total amount of work is not yet known.
type Progress struct {
	// Phase is one of: idle, error, info, download, model, separate, install.
	Phase   string  `json:"phase"`
	Percent float64 `json:"percent"`
	Label   string  `json:"label"`
	Detail  string  `json:"detail"`
}

// Busy tells the frontend whether to disable the action buttons.
type Busy struct {
	Busy  bool   `json:"busy"`
	Phase string `json:"phase"`
}

type Bus struct {
	mu      sync.Mutex
	ctx     context.Context
	history []LogLine
	last    Progress
}

func New() *Bus { return &Bus{last: Progress{Phase: "idle", Percent: -1}} }

// Attach stores the Wails runtime context; events emitted before this are kept
// in history only.
func (b *Bus) Attach(ctx context.Context) {
	b.mu.Lock()
	b.ctx = ctx
	b.mu.Unlock()
}

func (b *Bus) Logf(level, format string, args ...any) {
	b.Log(level, fmt.Sprintf(format, args...))
}

func (b *Bus) Log(level, text string) {
	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	line := LogLine{Time: time.Now().Format("15:04:05"), Level: level, Text: text}

	b.mu.Lock()
	b.history = append(b.history, line)
	if len(b.history) > 2000 {
		b.history = append([]LogLine(nil), b.history[len(b.history)-1500:]...)
	}
	ctx := b.ctx
	b.mu.Unlock()

	if ctx != nil {
		wruntime.EventsEmit(ctx, EventLog, line)
	}
}

func (b *Bus) History() []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]LogLine(nil), b.history...)
}

func (b *Bus) ClearHistory() {
	b.mu.Lock()
	b.history = nil
	b.mu.Unlock()
}

// Progress emits an update, coalescing identical consecutive states so a tqdm
// bar redrawing 50x/second does not flood the webview.
func (b *Bus) Progress(p Progress) {
	b.mu.Lock()
	if sameProgress(b.last, p) {
		b.mu.Unlock()
		return
	}
	b.last = p
	ctx := b.ctx
	b.mu.Unlock()

	if ctx != nil {
		wruntime.EventsEmit(ctx, EventProgress, p)
	}
}

func (b *Bus) LastProgress() Progress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last
}

func (b *Bus) Idle() {
	b.Progress(Progress{Phase: "idle", Percent: -1})
}

func (b *Bus) SetBusy(busy bool, phase string) {
	b.mu.Lock()
	ctx := b.ctx
	b.mu.Unlock()
	if ctx != nil {
		wruntime.EventsEmit(ctx, EventBusy, Busy{Busy: busy, Phase: phase})
	}
}

func sameProgress(a, b Progress) bool {
	// Quantise to 0.1% — finer updates are invisible in the UI anyway.
	return a.Phase == b.Phase &&
		int(a.Percent*10) == int(b.Percent*10) &&
		a.Label == b.Label &&
		a.Detail == b.Detail
}
