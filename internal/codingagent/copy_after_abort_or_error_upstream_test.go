package codingagent_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi ede360e4d (#12121, issue #12119) test/suite/regressions/12119-copy-after-abort-or-error.test.ts "returns the previous answer after a
// provider error", through the two interactive callers of getLastAssistantText (interactive-mode.ts handleCopyCommand): the /copy
// command and the app.message.copy key. The session is a real coding.Session whose second reply is an empty message with stopReason
// error, as Pi's faux provider gives it; before the fix both said "No agent messages to copy yet." while the answer was on screen.
func TestCopyAfterAProviderErrorCopiesThePreviousAnswer(t *testing.T) {
	for _, via := range []struct {
		name string
		run  func(h *icodingagent.TestHarness)
	}{
		{"/copy", func(h *icodingagent.TestHarness) { h.Enter("/copy") }},
		{"copy key", func(h *icodingagent.TestHarness) { h.Key("\x18") }},
	} {
		t.Run(via.name, func(t *testing.T) {
			pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil, reply("Here is the answer.", ai.Usage{}), replyError("400 invalid_request_error"))
			copied := pair.harness.CaptureClipboard()
			for i, question := range []string{"first question", "second question"} {
				pair.harness.Do(func() { pair.harness.Enter(question) })
				deadline := time.Now().Add(10 * time.Second)
				for len(pair.provider.requestLog()) < i+1 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				pair.harness.WaitIdle(t, 10*time.Second)
			}
			if got := len(pair.provider.requestLog()); got != 2 {
				t.Fatalf("model requests = %d, want 2 (the answer and the failed turn)", got)
			}
			messages := pair.session.Messages()
			if last := messages[len(messages)-1].Assistant; last == nil || last.StopReason != ai.StopReasonError || len(last.Content) != 0 {
				t.Fatalf("last message = %+v, want an empty assistant message with stopReason error", messages[len(messages)-1])
			}
			pair.harness.Do(func() { via.run(pair.harness) })
			deadline := time.Now().Add(10 * time.Second)
			for len(copied()) == 0 && !strings.Contains(pair.harness.Chat(), "No agent messages to copy yet.") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := copied(); !slices.Equal(got, []string{"Here is the answer."}) {
				t.Fatalf("copied = %q, want the previous answer; chat:\n%s", got, pair.harness.Chat())
			}
			if chat := pair.harness.Chat(); !strings.Contains(chat, "Copied last agent message to clipboard") || strings.Contains(chat, "No agent messages to copy yet.") {
				t.Fatalf("chat lacks the copy confirmation:\n%s", chat)
			}
		})
	}
}

// A session with nothing to copy still says so: a fresh one, and one whose only assistant message is the failed turn (Pi run at
// ede360e4d: getLastAssistantText returns undefined for both).
func TestCopyWithNothingToCopySaysSo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replies []scriptedReply
	}{
		{"fresh session", nil},
		{"only a failed turn", []scriptedReply{replyError("400 invalid_request_error")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil, tc.replies...)
			copied := pair.harness.CaptureClipboard()
			if len(tc.replies) > 0 {
				pair.harness.Do(func() { pair.harness.Enter("first question") })
				deadline := time.Now().Add(10 * time.Second)
				for len(pair.provider.requestLog()) < 1 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				pair.harness.WaitIdle(t, 10*time.Second)
			}
			pair.harness.Do(func() { pair.harness.Enter("/copy") })
			deadline := time.Now().Add(10 * time.Second)
			for len(copied()) == 0 && !strings.Contains(pair.harness.Chat(), "No agent messages to copy yet.") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := copied(); len(got) != 0 {
				t.Fatalf("copied = %q, want nothing", got)
			}
			if chat := pair.harness.Chat(); !strings.Contains(chat, "No agent messages to copy yet.") {
				t.Fatalf("chat lacks the nothing-to-copy line:\n%s", chat)
			}
		})
	}
}
