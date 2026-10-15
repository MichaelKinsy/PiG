package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
)

// RequestMarkdownTransform runs an extension's registered display rewrite. A null answer keeps the input, as Pi does for a throwing or non-string transformer.
const RequestMarkdownTransform = "markdown_transform"

// MarkdownTransformPayload is the RequestMarkdownTransform argument.
type MarkdownTransformPayload struct {
	Markdown string                             `json:"markdown"`
	Context  extension.MarkdownTransformContext `json:"context"`
}

// markdownTransformProxy waits on the caller-owned off-loop generation. It does not cache across messages: two identical messages can have different results from a stateful transformer.
//
// pig divergence (D56): a transform the renderer inactivity boundary abandoned returns its input, and the proxy asks again for the same message and context after a renderRetry backoff, then marks the message changed through the context's Retry so its next frame transforms it again. Pi runs transformers in process, so its message always shows the transformed text.
type markdownTransformProxy struct {
	conn       func() *Conn
	inactivity time.Duration

	mu sync.Mutex
	// stalled closes when the body of a request the host abandoned returns, or its connection ends. While it is open the transformer is disabled.
	stalled <-chan struct{}
	// retries holds the transforms the boundary abandoned and nothing answered since, by message context. An entry without waiters is dropped when its retry comes due.
	retries map[markdownRetryKey]*markdownRetryEntry
}

// markdownRetryKey names the transform of one text in one display context; a message that shows other text, or at another width, is another transform.
type markdownRetryKey struct {
	messageType extension.MarkdownMessageType
	streaming   bool
	width       int
	size        int
	sum         [sha256.Size]byte
}

type markdownRetryEntry struct {
	// conn is the connection that abandoned the transform; an entry of an earlier connection (the extension restarted) is dropped, nothing on it can make the retry due.
	conn  *Conn
	retry renderRetry
	// inFlight: a retry's request is out; no other request for this transform is sent until it settles.
	inFlight bool
	// waiters mark the messages that asked for this transform changed when the retry comes due.
	waiters []markdowntransform.RetryRequester
}

// markdownRetryMaxWaiters bounds the messages one transform key remembers; messages with identical text and context are rare.
// pig divergence (D56): Pi runs transformers in process and has no abandoned transform to retry; the bound limits what one abandoned transform asks again.
const markdownRetryMaxWaiters = 256

func (e *markdownRetryEntry) wait(retry markdowntransform.RetryRequester) {
	if retry == nil {
		return
	}
	// A message asks again with every frame that transforms it during the backoff.
	if reflect.TypeOf(retry).Comparable() && slices.Contains(e.waiters, retry) {
		return
	}
	if len(e.waiters) == markdownRetryMaxWaiters {
		e.waiters = append(e.waiters[:0], e.waiters[1:]...)
	}
	e.waiters = append(e.waiters, retry)
}

// disabledLocked reports whether a body the host abandoned is still running. Pi's chain finishes one transformer body before it starts the next
// (markdown-transform.ts:18-29), so an abandoned body keeps the transformer out of later chains, which keep their markdown as for a throwing transformer (D56: the stalled generation is disabled).
func (p *markdownTransformProxy) disabledLocked() bool {
	if p.stalled == nil {
		return false
	}
	select {
	case <-p.stalled:
		p.stalled = nil
		return false
	default:
		return true
	}
}

// giveUp records that the transform key was not answered: the boundary abandoned its request (settled is that request's, or the stalled body's), or the transformer is disabled. It plans the retry. settled may end the backoff early only for the first abandonment in a row.
func (p *markdownTransformProxy) giveUp(conn *Conn, key markdownRetryKey, retry markdowntransform.RetryRequester, settled <-chan struct{}) {
	p.mu.Lock()
	if p.retries == nil {
		p.retries = map[markdownRetryKey]*markdownRetryEntry{}
	}
	e := p.retries[key]
	if e == nil {
		e = &markdownRetryEntry{}
		p.retries[key] = e
	}
	e.conn = conn
	e.inFlight = false
	e.wait(retry)
	epoch, backoff, early := e.retry.abandoned(p.inactivity, settled)
	p.mu.Unlock()
	conn.afterRetryBackoff(early, backoff, func() { p.retryDue(key, e, epoch) }, func() { p.connClosed(key, e) })
}

// retryDue makes the retry of abandonment epoch due and marks the messages that wait for it changed.
func (p *markdownTransformProxy) retryDue(key markdownRetryKey, entry *markdownRetryEntry, epoch uint64) {
	p.mu.Lock()
	e := p.retries[key]
	if e == nil || e != entry || !e.retry.comeDue(epoch) {
		p.mu.Unlock()
		return
	}
	waiters := e.waiters
	e.waiters = nil
	conn, due := e.conn, e.retry.epoch
	if len(waiters) == 0 {
		delete(p.retries, key)
	}
	p.mu.Unlock()
	if len(waiters) > 0 {
		// The messages were told. If none of them asks again (it shows other text by now), the entry does not stay for good.
		conn.afterRetryBackoff(nil, renderRetryMaxBackoff, func() { p.expire(key, e, due) }, func() { p.connClosed(key, e) })
	}
	for _, retry := range waiters {
		retry.RequestRetry()
	}
}

// connClosed drops e, whose connection closed: nothing on it can make its retry due. The messages that waited are marked changed, so they transform again on the connection that replaced it, or keep their markdown if none did.
func (p *markdownTransformProxy) connClosed(key markdownRetryKey, e *markdownRetryEntry) {
	p.mu.Lock()
	var waiters []markdowntransform.RetryRequester
	if p.retries[key] == e {
		waiters = e.waiters
		delete(p.retries, key)
	}
	p.mu.Unlock()
	for _, retry := range waiters {
		retry.RequestRetry()
	}
}

// expire drops e when it came due and nothing took the retry since.
func (p *markdownTransformProxy) expire(key markdownRetryKey, e *markdownRetryEntry, epoch uint64) {
	p.mu.Lock()
	if p.retries[key] == e && e.retry.due && e.retry.epoch == epoch && !e.inFlight {
		delete(p.retries, key)
	}
	p.mu.Unlock()
}

func (p *markdownTransformProxy) transform(markdown string, transformContext extension.MarkdownTransformContext) string {
	conn := p.conn()
	if conn == nil {
		return markdown
	}
	// The key hashes the text, so it is made only when a retry can exist or the request gave up.
	var key markdownRetryKey
	keyed := false
	makeKey := func() markdownRetryKey {
		if !keyed {
			key = markdownRetryKey{messageType: transformContext.MessageType, streaming: transformContext.IsStreaming, width: transformContext.AvailableWidth, size: len(markdown), sum: sha256.Sum256([]byte(markdown))}
			keyed = true
		}
		return key
	}
	p.mu.Lock()
	var e *markdownRetryEntry
	if len(p.retries) > 0 {
		e = p.retries[makeKey()]
		if e != nil && e.conn != conn {
			// The extension restarted: nothing on the old connection can make this retry due.
			delete(p.retries, makeKey())
			e = nil
		}
	}
	switch {
	case e != nil:
		if e.inFlight || !e.retry.takeDue() {
			e.wait(transformContext.Retry)
			p.mu.Unlock()
			return markdown
		}
		// A retry is asked even while the abandoned body still runs, as a render proxy asks its extension: the backoff, not the body, bounds the requests.
		e.inFlight = true
	case p.disabledLocked():
		// pig divergence (D56): while a body the renderer inactivity boundary abandoned still runs, later chains skip this transformer instead of waiting for it.
		settled := p.stalled
		p.mu.Unlock()
		p.giveUp(conn, makeKey(), transformContext.Retry, settled)
		return markdown
	}
	p.mu.Unlock()

	ctx := transformContext.Context
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := json.Marshal(MarkdownTransformPayload{Markdown: markdown, Context: transformContext})
	if err != nil {
		return p.answered(makeKey, markdown, nil)
	}
	resp, err := conn.requestWithInactivity(ctx, &Envelope{
		Type:    MsgRequest,
		Request: &RequestPayload{Method: RequestMarkdownTransform, Args: args},
	}, p.inactivity, RequestMarkdownTransform)
	if stalled, ok := errors.AsType[*HandlerStalledError](err); ok {
		if stalled.Settled != nil {
			p.mu.Lock()
			p.stalled = stalled.Settled
			p.mu.Unlock()
		}
		p.giveUp(conn, makeKey(), transformContext.Retry, stalled.Settled)
		return markdown
	}
	if err != nil || resp == nil || resp.Response == nil || resp.Response.Error != nil {
		return p.answered(makeKey, markdown, nil)
	}
	var transformed *string
	if err := json.Unmarshal(resp.Response.Result, &transformed); err != nil || transformed == nil {
		return p.answered(makeKey, markdown, nil)
	}
	return p.answered(makeKey, markdown, transformed)
}

// answered ends the retries of key: the extension answered, or the request failed for a reason the boundary did not cause. It returns the answer, or markdown when there is none. Messages that waited for the retry meanwhile transform again.
func (p *markdownTransformProxy) answered(makeKey func() markdownRetryKey, markdown string, transformed *string) string {
	p.mu.Lock()
	var waiters []markdowntransform.RetryRequester
	if len(p.retries) > 0 {
		key := makeKey()
		if e := p.retries[key]; e != nil {
			e.retry.answered()
			waiters = e.waiters
			delete(p.retries, key)
		}
	}
	p.mu.Unlock()
	for _, retry := range waiters {
		retry.RequestRetry()
	}
	if transformed == nil {
		return markdown
	}
	return *transformed
}
