// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// CoreError is an error the core answered with: Pi's `{name, message}` (ABI section 8).
type CoreError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
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
}

// NewClient returns a client of one host.
func NewClient(host *sqlhost.Host) *Client { return &Client{host: host} }

// noticeID is the u32 that starts an api_result or tx_result payload.
func noticeID(n abi.Notice) (uint32, []byte, bool) {
	if len(n.Payload) < 4 {
		return 0, nil, false
	}
	return binary.LittleEndian.Uint32(n.Payload), n.Payload[4:], true
}

// request registers the wait, sends the event and waits for the answer, so no answer can arrive unseen.
func (c *Client) request(ctx context.Context, kind abi.NoticeKind, id uint32, ev abi.Event, accept func(body []byte) bool) ([]byte, error) {
	expect, err := c.host.Expect(func(n abi.Notice) bool {
		got, body, ok := noticeID(n)
		return n.Kind == kind && ok && got == id && accept(body)
	})
	if err != nil {
		return nil, err
	}
	if err := c.host.Send(ctx, ev); err != nil {
		expect.Cancel()
		return nil, err
	}
	notice, err := expect.Wait(ctx)
	if err != nil {
		return nil, err
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
	return answer(reply)
}

// Inspect runs an inspect query (ABI section 5, kind 16); the core echoes the request ID in an api_result notice.
func (c *Client) Inspect(ctx context.Context, query map[string]any) (json.RawMessage, error) {
	return c.echoed(ctx, abi.EventInspect, query)
}

// Abort sends an abort that expects an answer (abortSubmission, abortTask).
func (c *Client) Abort(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.echoed(ctx, abi.EventAbort, body)
}

func (c *Client) echoed(ctx context.Context, kind abi.EventKind, body map[string]any) (json.RawMessage, error) {
	id := c.requests.Add(1)
	withID := make(map[string]any, len(body)+1)
	maps.Copy(withID, body)
	withID["requestId"] = id
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
}

// ErrTxEnded is returned by an operation on a transaction that ended.
var ErrTxEnded = errors.New("the transaction has ended")

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
		return nil, err
	}
	kind, value, err := txBody(body)
	if err != nil {
		return nil, err
	}
	if kind == "rejected" {
		return nil, remote(value)
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

// End commits. state is the next task state that goes with the commit; nil sends none. It returns once the commit is
// applied; a rejected step is Pi's rejection.
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
	//portlint:allow mapkeyorder ABI 1 request bodies are read by field name by the core scanner; no reader depends on key order
	encoded, err := json.Marshal(op)
	if err != nil {
		return err
	}
	return t.client.host.Send(ctx, abi.Event{Kind: abi.EventTx, ID: t.id, Payload: encoded})
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
	return t.client.host.Send(ctx, abi.Event{Kind: abi.EventTx, ID: t.id, Payload: encoded})
}
