package subprocess

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// retryCounter is a message's Retry: it counts the times the host marked the message changed, as the interactive mode's block does.
type retryCounter struct{ n atomic.Int32 }

func (r *retryCounter) RequestRetry() { r.n.Add(1) }

func newRetryTransformProxy(ext *renderExtension, inactivity time.Duration) *markdownTransformProxy {
	return &markdownTransformProxy{conn: func() *Conn { return ext.conn }, inactivity: inactivity}
}

var markdownRetryContexts = []extension.MarkdownTransformContext{
	{MessageType: extension.MarkdownMessageUser, AvailableWidth: 40},
	{MessageType: extension.MarkdownMessageAssistant, IsStreaming: true, AvailableWidth: 40},
	{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 40},
	{MessageType: extension.MarkdownMessageAssistantThinking, AvailableWidth: 40},
}

func contextName(c extension.MarkdownTransformContext) string {
	return string(c.MessageType) + "/streaming=" + strconv.FormatBool(c.IsStreaming)
}

// transformAsync runs one transform on its own goroutine, as the TUI's off-loop generation does, and returns the channel of its result.
func transformAsync(proxy *markdownTransformProxy, markdown string, ctx extension.MarkdownTransformContext) <-chan string {
	out := make(chan string, 1)
	go func() { out <- proxy.transform(markdown, ctx) }()
	return out
}

// Pi runs transformers in process, so a message always shows its transformed text. When the renderer inactivity boundary (D56) abandons a
// transform, the message keeps its markdown only until the extension can answer: the proxy marks the message changed, and the frame that
// follows transforms it again. The message is drawn through a container and a Markdown cache whose key is the retry count, as the TUI draws it.
func TestMarkdownTransformRendersAgainOnceTheExtensionAnswersAnAbandonedTransform(t *testing.T) {
	const inactivity = time.Second
	for _, base := range markdownRetryContexts {
		t.Run(contextName(base), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ext := startRenderExtension(t)
				proxy := newRetryTransformProxy(ext, inactivity)
				retry := &retryCounter{}
				ctx := base
				ctx.Retry = retry
				md := tui.NewMarkdown("plain body")
				md.Transform = func(markdown string, _ int) string { return proxy.transform(markdown, ctx) }
				md.TransformState = func() string { return strconv.Itoa(int(retry.n.Load())) }
				card := tui.NewContainer(md)
				render := func() <-chan string {
					out := make(chan string, 1)
					go func() { out <- strings.Join(card.Render(40), "\n") }()
					return out
				}

				first := render()
				request := ext.next(t, MsgRequest)
				ext.abandon(t, request, inactivity)
				if got := <-first; !strings.Contains(got, "plain body") {
					t.Fatalf("frame after the give-up = %q, want the untransformed message", got)
				}
				// The extension answers the abandoned transform late; the host has given up on it.
				ext.respond(t, request, "late")
				synctest.Wait()
				if got := retry.n.Load(); got != 1 {
					t.Fatalf("the message was marked changed %d times after the extension answered the abandoned transform, want 1", got)
				}
				second := render()
				request = ext.next(t, MsgRequest)
				ext.respond(t, request, "transformed body")
				synctest.Wait()
				if got := <-second; !strings.Contains(got, "transformed body") || strings.Contains(got, "plain body") {
					t.Fatalf("frame after the retry = %q, want the transformed message", got)
				}
				if got := strings.Join(card.Render(40), "\n"); !strings.Contains(got, "transformed body") {
					t.Fatalf("a later frame = %q, want the transformed message kept", got)
				}
				if n := ext.sent(); n != 0 {
					t.Fatalf("the host sent %d more frames after the transform it needed", n)
				}
				if got := retry.n.Load(); got != 1 {
					t.Fatalf("the message was marked changed %d times in all, want 1", got)
				}
			})
		})
	}
}

// An extension that never answers gets one transform per backoff for a message, never one per frame and never two at once: the backoff
// starts at the inactivity boundary and doubles with each abandonment in a row, and the message is marked changed when the backoff ends.
func TestMarkdownTransformBacksOffFromAnExtensionThatNeverAnswers(t *testing.T) {
	const inactivity = time.Second
	for _, base := range markdownRetryContexts {
		t.Run(contextName(base), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ext := startRenderExtension(t)
				proxy := newRetryTransformProxy(ext, inactivity)
				retry := &retryCounter{}
				ctx := base
				ctx.Retry = retry
				frames := func() {
					for range 100 {
						if got := proxy.transform("body", ctx); got != "body" {
							t.Errorf("transform = %q, want the input while the extension is not answering", got)
						}
					}
				}
				first := transformAsync(proxy, "body", ctx)
				request := ext.next(t, MsgRequest)
				for i, backoff := range []time.Duration{inactivity, 2 * inactivity, 4 * inactivity} {
					ext.abandon(t, request, inactivity)
					if i == 0 {
						<-first
					}
					time.Sleep(backoff - time.Millisecond)
					frames()
					if n, want := ext.sent(), 0; n != want || retry.n.Load() != int32(i) {
						t.Fatalf("within the %s backoff: %d requests, %d retries; want 0 and %d", backoff, n, retry.n.Load(), i)
					}
					time.Sleep(time.Millisecond)
					synctest.Wait()
					if got := retry.n.Load(); got != int32(i+1) {
						t.Fatalf("after the %s backoff the message was marked changed %d times, want %d", backoff, got, i+1)
					}
					transformAsync(proxy, "body", ctx)
					request = ext.next(t, MsgRequest)
					frames()
					if n := ext.sent(); n != 0 {
						t.Fatalf("100 transforms after the %s backoff sent %d requests, want 1", backoff, n+1)
					}
				}
			})
		})
	}
}

// A transformer that always answers after the boundary transforms again at once only after the first abandonment in a row; from the second on
// its late answers do not cut the backoff short.
func TestMarkdownTransformBacksOffFromATransformerThatAlwaysAnswersLate(t *testing.T) {
	const inactivity = time.Second
	const late = inactivity / 2
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		proxy := newRetryTransformProxy(ext, inactivity)
		retry := &retryCounter{}
		ctx := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 40, Retry: retry}
		transformAsync(proxy, "body", ctx)
		request := ext.next(t, MsgRequest)
		ext.abandon(t, request, inactivity)
		time.Sleep(late)
		ext.respond(t, request, "late")
		synctest.Wait()
		if got := retry.n.Load(); got != 1 {
			t.Fatalf("after the late answer to the first abandonment the message was marked changed %d times, want 1", got)
		}
		transformAsync(proxy, "body", ctx)
		request = ext.next(t, MsgRequest)
		ext.abandon(t, request, inactivity)
		time.Sleep(late)
		ext.respond(t, request, "late")
		synctest.Wait()
		if got := retry.n.Load(); got != 1 {
			t.Fatalf("a late answer to the second abandonment in a row marked the message changed (%d), want the 2x backoff kept", got)
		}
		time.Sleep(2*inactivity - late - time.Millisecond)
		synctest.Wait()
		if got := retry.n.Load(); got != 1 {
			t.Fatalf("within the 2x backoff the message was marked changed %d times, want 1", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if got := retry.n.Load(); got != 2 {
			t.Fatalf("after the 2x backoff the message was marked changed %d times, want 2", got)
		}
	})
}

// Messages with the same text in the same context are one transform: one request serves them all, and each is marked changed.
func TestMarkdownTransformRetriesEveryMessageWithTheSameText(t *testing.T) {
	const inactivity = time.Second
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		proxy := newRetryTransformProxy(ext, inactivity)
		a, b := &retryCounter{}, &retryCounter{}
		ctxA := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageUser, AvailableWidth: 40, Retry: a}
		ctxB := ctxA
		ctxB.Retry = b
		firstA := transformAsync(proxy, "ok", ctxA)
		request := ext.next(t, MsgRequest)
		ext.abandon(t, request, inactivity)
		<-firstA
		if got := proxy.transform("ok", ctxB); got != "ok" {
			t.Fatalf("second message's transform = %q, want its input while the extension is not answering", got)
		}
		if n := ext.sent(); n != 0 {
			t.Fatalf("the second message sent %d requests during the backoff", n)
		}
		ext.respond(t, request, "late")
		synctest.Wait()
		if a.n.Load() != 1 || b.n.Load() != 1 {
			t.Fatalf("messages marked changed %d and %d times, want 1 each", a.n.Load(), b.n.Load())
		}
		retryA := transformAsync(proxy, "ok", ctxA)
		request = ext.next(t, MsgRequest)
		if got := proxy.transform("ok", ctxB); got != "ok" {
			t.Fatalf("second message's transform while the retry is in flight = %q, want its input", got)
		}
		if n := ext.sent(); n != 0 {
			t.Fatalf("the second message sent %d requests while the retry was in flight", n)
		}
		ext.respond(t, request, "OK")
		if got := <-retryA; got != "OK" {
			t.Fatalf("retried transform = %q, want OK", got)
		}
		synctest.Wait()
		if b.n.Load() != 2 {
			t.Fatalf("the message that waited for the retry was marked changed %d times, want 2", b.n.Load())
		}
	})
}

// An extension that restarts while a transform waits for its retry is asked again on the new connection: nothing on the closed one can make the retry due.
func TestMarkdownTransformAsksTheRestartedExtensionForAnAbandonedTransform(t *testing.T) {
	const inactivity = time.Second
	synctest.Test(t, func(t *testing.T) {
		old, fresh := startRenderExtension(t), startRenderExtension(t)
		var current atomic.Pointer[Conn]
		current.Store(old.conn)
		proxy := &markdownTransformProxy{conn: current.Load, inactivity: inactivity}
		retry := &retryCounter{}
		ctx := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageUser, AvailableWidth: 40, Retry: retry}
		first := transformAsync(proxy, "body", ctx)
		request := old.next(t, MsgRequest)
		old.abandon(t, request, inactivity)
		<-first
		_ = old.conn.Close("restart")
		synctest.Wait()
		if got := retry.n.Load(); got != 1 {
			t.Fatalf("the message was marked changed %d times when its connection closed, want 1", got)
		}
		proxy.mu.Lock()
		kept := len(proxy.retries)
		proxy.mu.Unlock()
		if kept != 0 {
			t.Fatalf("%d retries kept for a closed connection, want 0", kept)
		}
		current.Store(fresh.conn)
		next := transformAsync(proxy, "body", ctx)
		request = fresh.next(t, MsgRequest)
		fresh.respond(t, request, "BODY")
		if got := <-next; got != "BODY" {
			t.Fatalf("transform after the restart = %q, want BODY", got)
		}
	})
}

// A retry that came due and that no message took (its text moved on) does not stay in the proxy.
func TestMarkdownTransformDropsARetryNoMessageTakes(t *testing.T) {
	const inactivity = time.Second
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		proxy := newRetryTransformProxy(ext, inactivity)
		ctx := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 40, Retry: &retryCounter{}}
		first := transformAsync(proxy, "partial", ctx)
		request := ext.next(t, MsgRequest)
		ext.abandon(t, request, inactivity)
		<-first
		time.Sleep(inactivity + renderRetryMaxBackoff + time.Second)
		synctest.Wait()
		proxy.mu.Lock()
		n := len(proxy.retries)
		proxy.mu.Unlock()
		if n != 0 {
			t.Fatalf("%d retries kept after the backoff and a full extra backoff without the message asking again, want 0", n)
		}
	})
}
