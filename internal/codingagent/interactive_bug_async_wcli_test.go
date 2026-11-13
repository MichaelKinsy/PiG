package codingagent

// pi: packages/coding-agent/src/modes/interactive/bug-report.ts

import (
	"context"
	"testing"
	"testing/synctest"
)

// Pi bug-report.ts:68-76: the summary runs behind a loader in the editor slot
// with loader.signal; Escape on the loader aborts it, reportBug awaits the
// aborted summary, restores the editor and reports the cancellation. Here the
// Escape key itself is driven through the modal input route: the summary work
// is cancelled, summarizeForBugReport does not return before the work has
// finished, and the editor slot holds the editor again when it does.
func TestBugSummaryEscapeCancelsJoinsAndRestoresTheEditor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newExtensionDialogProbe(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.runCtx = ctx
		h := &shutdownSummaryHandle{cancelled: make(chan struct{}), release: make(chan struct{})}
		m.opts.SessionHandle = h
		type result struct {
			cancelled bool
			err       error
		}
		done := make(chan result, 1)
		go func() {
			_, cancelled, err := m.summarizeForBugReport("model", "")
			done <- result{cancelled, err}
		}()
		synctest.Wait()
		if children := m.editorContainer.Children(); len(children) != 1 || children[0] == any(m.editor) {
			t.Fatalf("editor slot during the summary = %v, want the loader alone", children)
		}
		input, _ := m.modalRoute()
		input <- []byte("\x1b")
		synctest.Wait()
		select {
		case <-h.cancelled:
		default:
			t.Fatal("Escape did not cancel the summary work")
		}
		select {
		case <-done:
			t.Fatal("summarizeForBugReport returned before the cancelled work finished")
		default:
		}
		close(h.release)
		r := <-done
		if !r.cancelled || r.err != nil {
			t.Fatalf("result = cancelled %v, err %v; want a cancellation", r.cancelled, r.err)
		}
		if children := m.editorContainer.Children(); len(children) != 1 || children[0] != any(m.editor) {
			t.Fatalf("editor slot after the summary = %v, want the editor back", children)
		}
	})
}
