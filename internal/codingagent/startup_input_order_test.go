package codingagent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type startupSubmitRecorder struct {
	InteractiveSessionHandle
	mode   *InteractiveMode
	drafts []string
}

func (r *startupSubmitRecorder) RunInputHandlers(_ context.Context, text string, images []ai.ImageContent, _ extension.InputSource, _ string) (string, []ai.ImageContent, bool, error) {
	r.drafts = append(r.drafts, r.mode.editor.Text())
	return text, images, true, nil
}

// Pi getUserInput resumes in a microtask after the current StdinBuffer callback finishes, before the next terminal-read event. Sibling sequences from the same read stay synchronous.
func TestUserInputHandoffFollowsCurrentReadBeforeNextRead(t *testing.T) {
	for _, sameRead := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			m, ctx := newCustomEditorDispatchMode(t)
			t.Cleanup(m.abortFn)
			m.runCtx = ctx
			m.isIdle = true
			m.endStartupSubmitWindow()
			recorder := &startupSubmitRecorder{mode: m}
			m.opts.SessionHandle = recorder
			m.inputReadCh = make(chan inputChunk, 3)
			m.inputErrCh = make(chan error, 1)
			var readDone chan struct{}
			if sameRead {
				readDone = make(chan struct{})
				defer close(readDone)
			}
			m.inputReadCh <- inputChunk{data: []byte("first"), readDone: readDone}
			m.inputReadCh <- inputChunk{data: []byte("\r"), readDone: readDone}
			m.inputReadCh <- inputChunk{data: []byte("draft")}
			done := make(chan error, 1)
			go func() { done <- m.inputLoop(ctx, strings.NewReader("")) }()
			synctest.Wait()
			want := ""
			if sameRead {
				want = "draft"
			}
			if len(recorder.drafts) != 1 || recorder.drafts[0] != want {
				t.Errorf("sameRead=%v: drafts at prompt=%q, want [%q]", sameRead, recorder.drafts, want)
			}
			m.inputErrCh <- io.EOF
			if err := <-done; !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
		})
	}
}
