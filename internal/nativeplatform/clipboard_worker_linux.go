//go:build linux

package nativeplatform

// Ports packages/tui/src/native-platform.ts.
// Private-worker ownership follows packages/tui/native/linux/src/clipboard-worker.h.

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type clipboardReadResult struct {
	data      []byte
	available bool
	latin1    bool
	err       error
}

type clipboardReadTask struct {
	cancel            context.CancelFunc
	done              chan struct{}
	waiting, finished bool
	result            clipboardReadResult
}

// clipboardWorker owns at most one private operation. The caller's three-second wait is separate from the operation lifetime; late results are discarded before admitting another operation.
type clipboardWorker struct {
	mu   sync.Mutex
	task *clipboardReadTask
	read func(context.Context, bool) clipboardReadResult
}

func (w *clipboardWorker) run(ctx context.Context, image bool) clipboardReadResult {
	if err := ctx.Err(); err != nil {
		return clipboardReadResult{err: err}
	}
	w.mu.Lock()
	if w.task != nil {
		w.mu.Unlock()
		return clipboardReadResult{}
	}
	privateCtx, cancel := context.WithCancel(context.Background())
	task := &clipboardReadTask{cancel: cancel, done: make(chan struct{}), waiting: true}
	w.task = task
	w.mu.Unlock()
	go func() {
		result := w.read(privateCtx, image)
		cancel()
		w.mu.Lock()
		task.finished = true
		if task.waiting {
			task.result = result
		} else {
			w.task = nil
		}
		close(task.done)
		w.mu.Unlock()
	}()
	// clipboard-worker.h gives the shared two-second transfer deadline time to report its own error.
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-task.done:
	case <-timer.C:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	task.waiting = false
	result := clipboardReadResult{}
	if task.finished {
		result = task.result
		task.result = clipboardReadResult{}
		w.task = nil
	}
	if err := ctx.Err(); err != nil {
		return clipboardReadResult{err: err}
	}
	return result
}

// close requests cancellation at process shutdown without joining private work. Pi detaches its clipboard thread, so an uninterruptible setup call cannot prevent natural process exit.
func (w *clipboardWorker) close() {
	w.mu.Lock()
	task := w.task
	if task != nil {
		task.cancel()
	}
	w.mu.Unlock()
}

var linuxClipboardWorker = clipboardWorker{read: func(ctx context.Context, image bool) clipboardReadResult {
	display := os.Getenv("DISPLAY")
	if display == "" {
		return clipboardReadResult{}
	}
	clipboard, err := openX11Clipboard(ctx, display)
	if err != nil {
		return clipboardReadResult{}
	}
	defer clipboard.close()
	property, err := clipboard.readSelection(image)
	if err != nil {
		return clipboardReadResult{available: true, err: errX11Clipboard}
	}
	return clipboardReadResult{data: property.data, available: true, latin1: !image && property.typeID == 31}
}}

var linuxClipboardAPI = NativeClipboard{
	GetText: func(ctx context.Context) (*string, bool, error) {
		return clipboardTextValue(linuxClipboardWorker.run(ctx, false))
	},
	GetImage: func(ctx context.Context) ([]byte, bool, error) {
		result := linuxClipboardWorker.run(ctx, true)
		return result.data, result.available, result.err
	},
}

func platformClipboard() *NativeClipboard { return &linuxClipboardAPI }

func clipboardTextValue(result clipboardReadResult) (*string, bool, error) {
	if result.err != nil || result.data == nil {
		return nil, result.available, result.err
	}
	text := jsstring.FromUTF8(result.data)
	if result.latin1 {
		var converted strings.Builder
		for _, character := range result.data {
			converted.WriteRune(rune(character))
		}
		text = converted.String()
	}
	return &text, result.available, nil
}

// ShutdownClipboard requests private X11 cancellation without holding process exit behind uninterruptible native setup.
func ShutdownClipboard() { linuxClipboardWorker.close() }
