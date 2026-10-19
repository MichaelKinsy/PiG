// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	coresession "github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// CoreError is an error the core answered with: Pi's `{name, message}` (ABI section 8).
type CoreError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	// ConversationID is ConversationBusy's structured field (the wire body's conversationId); nil when the core sent none.
	ConversationID *int64 `json:"conversationId,omitempty"`
}

func (e *CoreError) Error() string { return e.Message }

// ErrorName is the Pi error class name the core reported.
func (e *CoreError) ErrorName() string { return e.Name }

// Client sends the requests of the host-to-core request plane: `api` requests (ABI section 8.1), inspect and abort
// requests that expect an answer, and the extension transaction channel (section 8). An answer is a notice delivered
// after the step that carried the request. Every request fails when the handle is discarded, with the host's error.
type Client struct {
	host     *sqlhost.Host
	requests atomic.Uint32
	txs      atomic.Uint32
	// poisoned is the commit failure the Session surfaced once: every later call reports the Session failed.
	poisoned atomic.Pointer[sqlhost.FatalError]
	// created is HarnessOptions.ConversationCreated: a transaction that created or forked a conversation runs it last, in that
	// transaction, after the core's built-in creation hook (harness.ts conversationCreated); an error fails the operation.
	created func(tx durable.Tx, conversation durable.ConversationRecord) error
}

// SessionFailedError is what every call after a failed commit returns: Pi's SessionFailed (session.ts GuardedStorage), with the
// store's failure as its cause. It is a *durable.SessionFailed and also matches the core's session.ErrSessionFailed. The handle
// itself is discarded (ABI section 4.6); this is how the driver Session reports that to a caller.
type SessionFailedError struct{ *durable.SessionFailed }

// Is matches the core's ErrSessionFailed.
func (e *SessionFailedError) Is(target error) bool { return target == coresession.ErrSessionFailed }

// Unwrap returns the *durable.SessionFailed, whose own cause is the store's failure.
func (e *SessionFailedError) Unwrap() error { return e.SessionFailed }

// causeOr reports a context's cancellation cause when err is that context ending a wait: Pi rejects such a wait with the signal's
// reason (awaitWithContext), which Go carries as context.Cause; a bare context.Canceled would hide it.
func causeOr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return context.Cause(ctx)
	}
	return err
}

// poisonedOr maps the discard of a handle whose commit failed to Pi's poisoned Session. The call whose commit failed returns
// the store's own failure, as Pi's does (its text carries the cause); every later call returns the PoisonedError.
func (c *Client) poisonedOr(err error) error {
	var fatal *sqlhost.FatalError
	if err == nil || !errors.As(err, &fatal) || fatal.Message != sqlhost.FatalCommitFailed {
		return err
	}
	if c.poisoned.CompareAndSwap(nil, fatal) {
		return err
	}
	return &SessionFailedError{durable.NewSessionFailed(fatal)}
}

// NewClient returns a client of one host.
func NewClient(host *sqlhost.Host) *Client { return &Client{host: host} }

// request registers the wait, sends the event and waits for the answer, so no answer can arrive unseen.
func (c *Client) request(ctx context.Context, kind abi.NoticeKind, id uint32, ev abi.Event, accept func(body []byte) bool) ([]byte, error) {
	expect, err := c.host.ExpectNotice(kind, id, accept)
	if err != nil {
		return nil, c.poisonedOr(err)
	}
	if err := c.host.Send(ctx, ev); err != nil {
		expect.Cancel()
		return nil, c.poisonedOr(causeOr(ctx, err))
	}
	notice, err := expect.Wait(ctx)
	if err != nil {
		return nil, c.poisonedOr(causeOr(ctx, err))
	}
	return notice.Payload[4:], nil
}

func always([]byte) bool { return true }

// answer decodes `{ok}` or `{error}`.
func answer(body []byte) (json.RawMessage, error) {
	var a struct {
		OK    json.RawMessage `json:"ok"`
		Error *CoreError      `json:"error"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("api answer: %w", err)
	}
	if a.Error != nil {
		return nil, a.Error
	}
	return a.OK, nil
}

func merge(op string, args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+1)
	maps.Copy(out, args)
	out["op"] = op
	return out
}

// API runs one api request and returns the `ok` JSON. A core error is a *CoreError.
func (c *Client) API(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
	//portlint:allow mapkeyorder ABI 1 request bodies are read by field name by the core scanner; no reader depends on key order
	body, err := json.Marshal(merge(op, args))
	if err != nil {
		return nil, err
	}
	id := c.requests.Add(1)
	reply, err := c.request(ctx, abi.NoticeAPIResult, id, abi.Event{Kind: abi.EventAPI, ID: id, Payload: body}, always)
	if err != nil {
		return nil, err
	}
	ok, err := answer(reply)
	if err != nil || op != "context" {
		return ok, err
	}
	// The core names a context read's context instead of building it (ABI section 8.1): this host derives it.
	var named struct {
		Context *struct {
			Conversation int64 `json:"conversationId"`
			Tail         int64 `json:"tail"`
		} `json:"$context"`
	}
	if json.Unmarshal(ok, &named) != nil || named.Context == nil {
		return ok, nil
	}
	return c.host.ContextView(ctx, named.Context.Conversation, named.Context.Tail)
}

// Inspect runs an inspect query (ABI section 5, kind 16); the core echoes the request ID in an api_result notice.
func (c *Client) Inspect(ctx context.Context, query map[string]any) (json.RawMessage, error) {
	return c.echoed(ctx, abi.EventInspect, query)
}

// Abort sends an abort that expects an answer (abortSubmission, abortTask).
func (c *Client) Abort(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.echoed(ctx, abi.EventAbort, body)
}

// Submit sends a submit event (ABI section 5, kind 3); the core answers with an api_result notice for the event's id,
// {ok: {id: submissionId}}, after the commit's published notice.
func (c *Client) Submit(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.echoedAs(ctx, abi.EventSubmit, "id", body)
}

func (c *Client) echoed(ctx context.Context, kind abi.EventKind, body map[string]any) (json.RawMessage, error) {
	return c.echoedAs(ctx, kind, "requestId", body)
}

func (c *Client) echoedAs(ctx context.Context, kind abi.EventKind, key string, body map[string]any) (json.RawMessage, error) {
	id := c.requests.Add(1)
	withID := make(map[string]any, len(body)+1)
	maps.Copy(withID, body)
	withID[key] = id
	//portlint:allow mapkeyorder ABI 1 request bodies are read by field name by the core scanner; no reader depends on key order
	encoded, err := json.Marshal(withID)
	if err != nil {
		return nil, err
	}
	reply, err := c.request(ctx, abi.NoticeAPIResult, id, abi.Event{Kind: kind, Payload: encoded}, always)
	if err != nil {
		return nil, err
	}
	return answer(reply)
}

// Tx is one extension transaction: the Session line is held from Begin to End or Abort, across calls. Operations run
// one at a time.
type Tx struct {
	client *Client
	id     uint32
	mu     sync.Mutex
	done   bool
	// Current is the task record a transaction begun for an invocation read when it took the line (ready.current); nil
	// for a Session commit.
	Current json.RawMessage
}

// ErrTxEnded is returned by an operation on a transaction that ended.
var ErrTxEnded = errors.New("Transaction has settled")

// txBody classifies a tx_result body: ready, waiting, result or rejected.
func txBody(body []byte) (kind string, value json.RawMessage, err error) {
	var a map[string]json.RawMessage
	if err := json.Unmarshal(body, &a); err != nil {
		return "", nil, fmt.Errorf("tx answer: %w", err)
	}
	for _, k := range []string{"ready", "waiting", "result", "rejected"} {
		if v, ok := a[k]; ok {
			return k, v, nil
		}
	}
	return "", nil, fmt.Errorf("tx answer %s names no known outcome", body)
}

func isFinal(body []byte) bool {
	kind, _, err := txBody(body)
	return err != nil || kind == "result" || kind == "rejected"
}

func isReady(body []byte) bool {
	kind, _, err := txBody(body)
	return err != nil || kind == "ready" || kind == "rejected"
}

func remote(value json.RawMessage) error {
	var e CoreError
	if err := json.Unmarshal(value, &e); err != nil {
		return fmt.Errorf("tx rejection: %w", err)
	}
	return &e
}

// Begin opens a transaction. It returns at `ready`; a `waiting` answer only means the line is held and `ready` follows.
// Options are `invocation` and `conversationId`.
func (c *Client) Begin(ctx context.Context, options map[string]any) (*Tx, error) {
	tx := &Tx{client: c, id: c.txs.Add(1)}
	body, err := tx.reply(ctx, merge("begin", options), isReady)
	if err != nil {
		if ctx.Err() != nil {
			// The wait ended with the caller's context, but the begin event is queued or already ran: the core takes its turn on
			// the Session line for a transaction nobody will end unless it is told to drop it. Without the abort, the line is
			// granted to this transaction for good and every later commit, and Close, queues behind it.
			_ = tx.Abort(context.WithoutCancel(ctx), err)
		}
		return nil, err
	}
	kind, value, err := txBody(body)
	if err != nil {
		return nil, err
	}
	if kind == "rejected" {
		return nil, remote(value)
	}
	var ready struct {
		Current json.RawMessage `json:"current"`
	}
	if err := json.Unmarshal(body, &ready); err == nil {
		tx.Current = ready.Current
	}
	return tx, nil
}

// Bind returns the transaction a callback effect carries (ABI effect 13): the core already opened it.
func (c *Client) Bind(txID uint32) *Tx { return &Tx{client: c, id: txID} }

func (t *Tx) reply(ctx context.Context, op map[string]any, accept func([]byte) bool) ([]byte, error) {
	//portlint:allow mapkeyorder ABI 1 request bodies are read by field name by the core scanner; no reader depends on key order
	encoded, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	return t.client.request(ctx, abi.NoticeTxResult, t.id, abi.Event{Kind: abi.EventTx, ID: t.id, Payload: encoded}, accept)
}

// Op runs one Tx operation and returns its `result`. A rejection is a *CoreError.
func (t *Tx) Op(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil, ErrTxEnded
	}
	body, err := t.reply(ctx, merge(op, args), isFinal)
	if err != nil {
		return nil, err
	}
	kind, value, err := txBody(body)
	if err != nil {
		return nil, err
	}
	if kind == "rejected" {
		return nil, remote(value)
	}
	return value, nil
}

// End commits. state is the next task state that goes with the commit; nil sends none. It returns when the core answers
// {result: null}, at the end of the commit's job on the Session line (ABI section 5.3); a rejected step is Pi's rejection.
func (t *Tx) End(ctx context.Context, state any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return ErrTxEnded
	}
	t.done = true
	op := map[string]any{"op": "end"}
	if state != nil {
		op["state"] = state
	}
	body, err := t.reply(ctx, op, isFinal)
	if err != nil {
		return err
	}
	kind, value, err := txBody(body)
	if err != nil {
		return err
	}
	if kind == "rejected" {
		return remote(value)
	}
	return nil
}

// Abort rolls back: the callback failed with err.
func (t *Tx) Abort(ctx context.Context, cause error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil
	}
	t.done = true
	failure := CoreError{Name: "Error", Message: cause.Error()}
	if named, ok := errors.AsType[interface {
		error
		ErrorName() string
	}](cause); ok {
		failure.Name = named.ErrorName()
	}
	//portlint:allow mapkeyorder ABI 1 request bodies are read by field name by the core scanner; no reader depends on key order
	encoded, err := json.Marshal(map[string]any{"op": "abort", "error": failure})
	if err != nil {
		return err
	}
	return t.client.poisonedOr(t.client.host.Send(ctx, abi.Event{Kind: abi.EventTx, ID: t.id, Payload: encoded}))
}
