// Package viewcarrier carries the plugin UI attachment API of coding/uiview on JSON lines, so a client outside the
// process draws mounts and raises intents over a transport the host already serves. pig additive (D614).
//
// The carrier adds no record: every message is one call of the Go API with that call's records as its members.
//
//	ui_view_attach         Registry.Attach             reply {attachment, seq, snapshots}
//	ui_view_abilities      Attachment.UpdateAbilities  reply {}
//	ui_view_resync         Attachment.Resync           reply {attachment, seq, snapshots}
//	ui_view_detach         Attachment.Detach           reply {}
//	ui_view_intent         Attachment.Invoke           reply {ticket, outcome, answer, error} once the intent settled
//	ui_view_intent_cancel  ends the context of the intent request its id names; reply {}
//	ui_view_report         Attachment.Report           reply {}
//	ui_view (event)        one Delivery
//	ui_view_gap (event)    the sink refused a delivery
//
// One connection holds one attachment at a time. The sink only encodes and enqueues; one goroutine per attachment
// hands the queue to the connection's ordered output and waits for it there, so the registry lock and the loops that
// take it never wait on a client. An attach, abilities or resync reply is written before any event numbered after it.
package viewcarrier

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The commands and events of the carrier.
const (
	CommandAttach       = "ui_view_attach"
	CommandAbilities    = "ui_view_abilities"
	CommandResync       = "ui_view_resync"
	CommandDetach       = "ui_view_detach"
	CommandIntent       = "ui_view_intent"
	CommandIntentCancel = "ui_view_intent_cancel"
	CommandReport       = "ui_view_report"

	eventView = "ui_view"
	eventGap  = "ui_view_gap"
)

// pig additive (D614): the bounds of the carrier.
const (
	maxQueueEvents  = 256     // pig additive (D614): events of one attachment the client has not been written
	maxQueueBytes   = 4 << 20 // pig additive (D614): bytes of those events
	maxMessageBytes = 4 << 20 // pig additive (D614): bytes of one ui_view line, and of one command line
)

// Handles reports whether command is one of the carrier's. pig additive (D614): these seven names are commands for
// every client, attached or not; any other name is not the carrier's.
func Handles(command string) bool {
	switch command {
	case CommandAttach, CommandAbilities, CommandResync, CommandDetach, CommandIntent, CommandIntentCancel, CommandReport:
		return true
	}
	return false
}

// Host is the session whose mounts a client draws: its registry and the way an intent reaches the plugin.
type Host interface {
	Views() *uiview.Registry
	InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result
}

// Transport is the connection a carrier rides.
type Transport struct {
	// Host returns the current session's host, or nil when the session runs no extension host.
	Host func() Host
	// Write appends one encoded line to the connection's ordered output. It does not wait for the client.
	Write func(line []byte)
	// Flushed returns once every line written so far reached the client, or ctx ended.
	Flushed func(ctx context.Context) bool
}

// Reply writes a command's response in the transport's response order and then runs written, if any.
type Reply func(response any, written func())

// Response is the reply to a carrier command: Pi's rpc response, with the closed code beside the error text.
type Response struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    any             `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Code    uiview.Code     `json:"code,omitempty"`
}

type resyncData struct {
	Attachment uint64            `json:"attachment"`
	Seq        uint64            `json:"seq"`
	Snapshots  []json.RawMessage `json:"snapshots"`
}

// Carrier serves the carrier's commands for one connection.
type Carrier struct {
	tr Transport

	mu      sync.Mutex
	att     *attachment
	intents map[string]context.CancelFunc
	closed  bool
	work    sync.WaitGroup
}

// New returns the carrier of one connection.
func New(tr Transport) *Carrier {
	return &Carrier{tr: tr, intents: map[string]context.CancelFunc{}}
}

type queued struct {
	seq  uint64
	line []byte
}

type report struct {
	plugin string
	ref    uiview.Ref
	notice uiview.Notice
}

// attachment is one client's hold and the events it has not been written. The carrier's mutex guards every field
// below t; the registry calls sink under its own lock, so nothing here calls the registry while it holds the mutex.
type attachment struct {
	c    *Carrier
	host Host
	reg  *uiview.Registry
	t    *uiview.Attachment
	ctx  context.Context
	stop context.CancelFunc
	wake *sync.Cond

	queue   []queued
	bytes   int
	reports []report
	// flight counts the events handed to the output that have not reached the client yet.
	flight, flightBytes int
	// put numbers the events and gap lines queued for the client. done is the number up to which each of them was handed
	// to the output or dropped by a resync, and dropped the number a resync dropped up to while the writer was handing a
	// batch over (writing), so a reply can wait for the events that were queued before it.
	put, done, dropped uint64
	writing            bool
	// hold is above zero while a reply that must precede the queued events is unwritten.
	hold    int
	gapOwed bool
	gapSeq  uint64
	ended   bool
}

func failure(id json.RawMessage, command string, err *uiview.Error) Response {
	return Response{ID: id, Type: "response", Command: command, Error: err.Error(), Code: err.Code}
}

func success(id json.RawMessage, command string, data any) Response {
	return Response{ID: id, Type: "response", Command: command, Success: true, Data: data}
}

func invalid(text string) *uiview.Error {
	return &uiview.Error{Code: uiview.CodeInvalid, Message: text}
}

// strict returns a decoder that reads a command strictly: an unknown member rejects it. Each command decodes into its
// own struct where it is declared, so no decode has an interface value as its destination (make any-trap).
func strict(raw []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec
}

// malformed is the answer to a command strict could not decode, or nil when it could.
func malformed(err error) *uiview.Error {
	if err == nil {
		return nil
	}
	return invalid(strings.TrimPrefix(err.Error(), "json: "))
}

// Handle serves one command line. It never waits on the client; an intent replies from its own goroutine once settled.
func (c *Carrier) Handle(id json.RawMessage, command string, raw []byte, reply Reply) {
	if len(raw) > maxMessageBytes {
		reply(failure(id, command, &uiview.Error{Code: uiview.CodeLimit, Message: "a command line is at most " + strconv.Itoa(maxMessageBytes) + " bytes"}), nil)
		return
	}
	switch command {
	case CommandAttach:
		c.attach(id, raw, reply)
	case CommandAbilities:
		c.abilities(id, raw, reply)
	case CommandResync:
		c.resync(id, raw, reply)
	case CommandDetach:
		c.detach(id, raw, reply)
	case CommandIntent:
		c.intent(id, raw, reply)
	case CommandIntentCancel:
		c.intentCancel(id, raw, reply)
	case CommandReport:
		c.report(id, raw, reply)
	}
}

// Close ends the connection's attachment, which settles its intents in flight stale, and waits for the carrier's
// goroutines.
func (c *Carrier) Close() {
	c.mu.Lock()
	c.closed = true
	a := c.att
	c.att = nil
	c.mu.Unlock()
	if a != nil {
		a.end()
	}
	c.work.Wait()
}

// held returns the connection's attachment when n names it and it belongs to the current session. An attachment of a
// session that was replaced is ended here: the client attaches again, to the new session's registry.
func (c *Carrier) held(n *uint64) *attachment {
	host := c.tr.Host()
	c.mu.Lock()
	a := c.att
	if a != nil && (host == nil || host.Views() != a.reg) {
		c.att = nil
		c.mu.Unlock()
		a.end()
		return nil
	}
	c.mu.Unlock()
	if a == nil || n == nil || *n != a.t.Generation() {
		return nil
	}
	return a
}

func stale(n *uint64) *uiview.Error {
	if n == nil {
		return invalid("attachment is required")
	}
	return &uiview.Error{Code: uiview.CodeStale, Message: "the connection does not hold attachment " + strconv.FormatUint(*n, 10)}
}

func (c *Carrier) attach(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID        json.RawMessage         `json:"id"`
		Type      string                  `json:"type"`
		Abilities *uiview.ClientAbilities `json:"abilities"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandAttach, err), nil)
		return
	}
	if cmd.Abilities == nil {
		reply(failure(id, CommandAttach, invalid("abilities is required")), nil)
		return
	}
	host := c.tr.Host()
	if host == nil {
		reply(failure(id, CommandAttach, &uiview.Error{Code: uiview.CodeUnsupported, Message: "the session runs no extension host"}), nil)
		return
	}
	reg := host.Views()
	c.mu.Lock()
	old := c.att
	if c.closed {
		c.mu.Unlock()
		reply(failure(id, CommandAttach, &uiview.Error{Code: uiview.CodeStale, Message: "the connection ended"}), nil)
		return
	}
	if old != nil && old.reg == reg {
		c.mu.Unlock()
		// pig additive (D614): one attachment per connection at a time.
		reply(failure(id, CommandAttach, &uiview.Error{Code: uiview.CodeLimit, Message: "the connection already holds an attachment"}), nil)
		return
	}
	c.att = nil
	c.mu.Unlock()
	if old != nil {
		old.end()
	}
	a := &attachment{c: c, host: host, reg: reg, hold: 1}
	a.wake = sync.NewCond(&c.mu)
	//portlint:allow ctxbackground this context is the attachment's own lifetime: its end stops the writer, and no command's context outlives an attachment
	a.ctx, a.stop = context.WithCancel(context.Background())
	t, rs, err := reg.Attach(*cmd.Abilities, a.sink)
	if err != nil {
		a.stop()
		reply(failure(id, CommandAttach, err), nil)
		return
	}
	a.t = t
	c.mu.Lock()
	c.att = a
	c.mu.Unlock()
	c.work.Go(a.pump)
	reply(success(id, CommandAttach, a.resyncData(rs)), a.release)
}

func (c *Carrier) abilities(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID         json.RawMessage         `json:"id"`
		Type       string                  `json:"type"`
		Attachment *uint64                 `json:"attachment"`
		Abilities  *uiview.ClientAbilities `json:"abilities"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandAbilities, err), nil)
		return
	}
	if cmd.Abilities == nil {
		reply(failure(id, CommandAbilities, invalid("abilities is required")), nil)
		return
	}
	a := c.held(cmd.Attachment)
	if a == nil {
		reply(failure(id, CommandAbilities, stale(cmd.Attachment)), nil)
		return
	}
	a.holdEvents(false)
	if err := a.t.UpdateAbilities(*cmd.Abilities); err != nil {
		a.release()
		reply(failure(id, CommandAbilities, err), nil)
		return
	}
	reply(success(id, CommandAbilities, struct{}{}), a.release)
}

func (c *Carrier) resync(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID         json.RawMessage `json:"id"`
		Type       string          `json:"type"`
		Attachment *uint64         `json:"attachment"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandResync, err), nil)
		return
	}
	a := c.held(cmd.Attachment)
	if a == nil {
		reply(failure(id, CommandResync, stale(cmd.Attachment)), nil)
		return
	}
	// Every event queued now is numbered at or below the number the resync returns, and the client drops those.
	a.holdEvents(true)
	rs, err := a.t.Resync()
	if err != nil {
		a.release()
		reply(failure(id, CommandResync, err), nil)
		return
	}
	reply(success(id, CommandResync, a.resyncData(rs)), a.release)
}

func (c *Carrier) detach(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID         json.RawMessage `json:"id"`
		Type       string          `json:"type"`
		Attachment *uint64         `json:"attachment"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandDetach, err), nil)
		return
	}
	a := c.held(cmd.Attachment)
	if a == nil {
		reply(failure(id, CommandDetach, stale(cmd.Attachment)), nil)
		return
	}
	c.mu.Lock()
	c.att = nil
	c.mu.Unlock()
	a.end()
	reply(success(id, CommandDetach, struct{}{}), nil)
}

func (c *Carrier) intent(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID         json.RawMessage `json:"id"`
		Type       string          `json:"type"`
		Attachment *uint64         `json:"attachment"`
		Plugin     string          `json:"plugin"`
		Mount      *uiview.Ref     `json:"mount"`
		NodeKey    string          `json:"nodeKey"`
		Intent     string          `json:"intent"`
		Arg        json.RawMessage `json:"arg"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandIntent, err), nil)
		return
	}
	switch {
	case cmd.Attachment == nil:
		reply(failure(id, CommandIntent, invalid("attachment is required")), nil)
		return
	case cmd.Plugin == "" || cmd.Mount == nil || cmd.Intent == "" || cmd.Arg == nil:
		reply(failure(id, CommandIntent, invalid("plugin, mount, intent and arg are required")), nil)
		return
	}
	a := c.held(cmd.Attachment)
	if a == nil {
		reply(success(id, CommandIntent, uiview.Result{Outcome: uiview.OutcomeStale, Error: "attachment ended"}), nil)
		return
	}
	//portlint:allow ctxbackground an intent outlives the command that raised it: the request cancel by id ends it, and the attachment's end settles it
	ctx, cancel := context.WithCancel(context.Background())
	key := string(id)
	c.mu.Lock()
	if key != "" {
		if _, busy := c.intents[key]; busy {
			c.mu.Unlock()
			cancel()
			reply(failure(id, CommandIntent, invalid("an intent in flight has this id")), nil)
			return
		}
		c.intents[key] = cancel
	}
	c.mu.Unlock()
	c.work.Go(func() {
		res := a.host.InvokeViewIntentFrom(ctx, a.t, cmd.Plugin, *cmd.Mount, cmd.NodeKey, cmd.Intent, cmd.Arg)
		if key != "" {
			c.mu.Lock()
			delete(c.intents, key)
			c.mu.Unlock()
		}
		cancel()
		// What the handler changed while it answered is delivered before its answer: the client holds the new view when it
		// learns how the intent ended.
		a.written()
		reply(success(id, CommandIntent, res), nil)
	})
}

// intentCancel is the transport's request cancel: its id names the intent request, and the intent replies cancelled.
func (c *Carrier) intentCancel(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID   json.RawMessage `json:"id"`
		Type string          `json:"type"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandIntentCancel, err), nil)
		return
	}
	if len(id) == 0 {
		reply(failure(id, CommandIntentCancel, invalid("id is required")), nil)
		return
	}
	c.mu.Lock()
	cancel := c.intents[string(id)]
	c.mu.Unlock()
	if cancel == nil {
		reply(failure(id, CommandIntentCancel, &uiview.Error{Code: uiview.CodeNotFound, Message: "no intent in flight has this id"}), nil)
		return
	}
	// The cancel's own reply goes first, so a client reads it before the intent's cancelled result.
	reply(success(id, CommandIntentCancel, struct{}{}), nil)
	cancel()
}

func (c *Carrier) report(id json.RawMessage, raw []byte, reply Reply) {
	var cmd struct {
		ID         json.RawMessage `json:"id"`
		Type       string          `json:"type"`
		Attachment *uint64         `json:"attachment"`
		Plugin     string          `json:"plugin"`
		Mount      *struct {
			Key   string `json:"key"`
			Epoch uint64 `json:"epoch"`
		} `json:"mount"`
		Notice *struct {
			Code     string          `json:"code"`
			Severity uiview.Severity `json:"severity"`
			Message  string          `json:"message"`
			Path     string          `json:"path"`
			Claim    string          `json:"claim"`
			Measured *int64          `json:"measured"`
			Limit    *int64          `json:"limit"`
		} `json:"notice"`
	}
	if err := malformed(strict(raw).Decode(&cmd)); err != nil {
		reply(failure(id, CommandReport, err), nil)
		return
	}
	if cmd.Attachment == nil || cmd.Plugin == "" || cmd.Mount == nil || cmd.Notice == nil {
		reply(failure(id, CommandReport, invalid("attachment, plugin, mount and notice are required")), nil)
		return
	}
	a := c.held(cmd.Attachment)
	if a == nil {
		reply(failure(id, CommandReport, stale(cmd.Attachment)), nil)
		return
	}
	n := cmd.Notice
	notice := uiview.Notice{Code: n.Code, Severity: n.Severity, Message: n.Message, Path: n.Path, Claim: n.Claim, Measured: n.Measured, Limit: n.Limit}
	if err := a.t.Report(cmd.Plugin, uiview.Ref{Key: cmd.Mount.Key, Epoch: cmd.Mount.Epoch}, notice); err != nil {
		reply(failure(id, CommandReport, err), nil)
		return
	}
	reply(success(id, CommandReport, struct{}{}), nil)
}

// carried encodes a snapshot as the client is sent it: the ui_view line of delivery seq and, inside it, the snapshot.
// A snapshot the carrier cannot carry whole is carried degraded, without its content, and why says what the plugin is
// told: a line above the message bound (limit), or an asset the carrier has no way to serve (unsupported). ok is false
// when the snapshot has no encoding at all.
func carried(gen, seq uint64, s uiview.Snapshot) (line, snapshot []byte, why *uiview.Notice, ok bool) {
	// pig additive (D614): assets ride inline; a managed asset is not carried.
	if slices.ContainsFunc(s.Assets, func(a uiview.Asset) bool { return a.Delivery == "managed" }) {
		why = &uiview.Notice{Code: string(uiview.CodeUnsupported), Severity: uiview.SeverityWarning, Path: "delivery", Message: "a managed asset is not carried to this client"}
		degrade(&s)
	}
	head := make([]byte, 0, 96)
	head = append(head, `{"type":"`+eventView+`","attachment":`...)
	head = strconv.AppendUint(head, gen, 10)
	head = append(head, `,"seq":`...)
	head = strconv.AppendUint(head, seq, 10)
	head = append(head, `,"snapshot":`...)
	encode := func() []byte {
		line, err := jsstring.AppendMarshalJSON(slices.Clone(head), &s)
		if err != nil {
			return nil
		}
		return append(line, '}', '\n')
	}
	line = encode()
	if line == nil || len(line) > maxMessageBytes {
		measured, limit := int64(len(line)), int64(maxMessageBytes)
		why = &uiview.Notice{Code: string(uiview.CodeLimit), Severity: uiview.SeverityWarning, Path: "delivery", Message: "the snapshot is larger than one message", Measured: &measured, Limit: &limit}
		degrade(&s)
		if line = encode(); line == nil {
			return nil, nil, nil, false
		}
	}
	return line, line[len(head) : len(line)-2], why, true
}

// degrade empties a snapshot's content, as the registry does for content that does not fit a client and has no fallback.
func degrade(s *uiview.Snapshot) {
	s.Content = uiview.Content{}
	s.Alternative = 0
	s.Degraded = true
	s.Digest = ""
	if raw, err := json.Marshal(&s.Content); err == nil {
		s.Digest, _ = uiview.Digest(raw)
	}
}

func gapLine(gen, seq uint64) []byte {
	line := make([]byte, 0, 64)
	line = append(line, `{"type":"`+eventGap+`","attachment":`...)
	line = strconv.AppendUint(line, gen, 10)
	line = append(line, `,"seq":`...)
	line = strconv.AppendUint(line, seq, 10)
	return append(line, '}', '\n')
}

// sink takes a delivery under the registry lock: it encodes and enqueues, and refuses when the queue is full.
func (a *attachment) sink(d uiview.Delivery) bool {
	line, _, why, ok := carried(d.Attachment, d.Seq, d.Snapshot)
	c := a.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.ended {
		return false
	}
	if !ok {
		a.oweGap(d.Seq)
		return false
	}
	if len(a.queue)+a.flight >= maxQueueEvents || a.bytes+a.flightBytes+len(line) > maxQueueBytes {
		// The registry numbered this delivery, so it is the last number made.
		a.oweGap(d.Seq)
		return false
	}
	a.queue = append(a.queue, queued{d.Seq, line})
	a.put++
	a.bytes += len(line)
	if why != nil {
		a.reports = append(a.reports, report{d.Snapshot.Plugin, d.Snapshot.Mount, *why})
	}
	a.wake.Broadcast()
	return true
}

// oweGap queues the gap line of the refused delivery seq. The caller holds the mutex.
func (a *attachment) oweGap(seq uint64) {
	if !a.gapOwed {
		a.put++
	}
	a.gapOwed, a.gapSeq = true, seq
	a.wake.Broadcast()
}

// written returns once every event and gap line queued before the call was handed to the connection's output or
// dropped by a resync, or the attachment ended.
func (a *attachment) written() {
	c := a.c
	c.mu.Lock()
	for target := a.put; !a.ended && a.done < target; {
		a.wake.Wait()
	}
	c.mu.Unlock()
}

// pump is the attachment's one writer: it hands the queued events to the connection's output in order, then the gap
// line when the sink refused one, and waits until the client was written them before it takes more.
func (a *attachment) pump() {
	c := a.c
	for {
		c.mu.Lock()
		for !a.ended && (a.hold > 0 || (len(a.queue) == 0 && !a.gapOwed)) {
			a.wake.Wait()
		}
		if a.ended {
			c.mu.Unlock()
			return
		}
		batch, reports, gap, gapSeq := a.queue, a.reports, a.gapOwed, a.gapSeq
		a.flight, a.flightBytes = len(batch), a.bytes
		a.queue, a.reports, a.bytes, a.gapOwed = nil, nil, 0, false
		taken := a.put
		a.writing = true
		c.mu.Unlock()
		for _, q := range batch {
			c.tr.Write(q.line)
		}
		if gap {
			c.tr.Write(gapLine(a.t.Generation(), gapSeq))
		}
		c.mu.Lock()
		a.writing = false
		a.done = max(taken, a.dropped)
		a.wake.Broadcast()
		c.mu.Unlock()
		a.tell(reports)
		c.tr.Flushed(a.ctx)
		c.mu.Lock()
		a.flight, a.flightBytes = 0, 0
		c.mu.Unlock()
	}
}

// tell records for the plugin what the carrier could not carry; the plugin reads it with ui.view.diagnostics.
func (a *attachment) tell(reports []report) {
	for _, r := range reports {
		_ = a.t.Report(r.plugin, r.ref, r.notice)
	}
}

// holdEvents keeps the queued events back until release. With drop it also forgets the events and the gap line that
// are queued: a resync supersedes them.
func (a *attachment) holdEvents(drop bool) {
	a.c.mu.Lock()
	a.hold++
	if drop {
		a.queue, a.reports, a.bytes, a.gapOwed = nil, nil, 0, false
		a.dropped = a.put
		if !a.writing {
			a.done = a.put
		}
		a.wake.Broadcast()
	}
	a.c.mu.Unlock()
}

func (a *attachment) release() {
	a.c.mu.Lock()
	a.hold--
	a.wake.Broadcast()
	a.c.mu.Unlock()
}

// resyncData is the reply of attach and resync. Each snapshot obeys the bound of a ui_view line.
func (a *attachment) resyncData(rs uiview.Resync) resyncData {
	data := resyncData{Attachment: rs.Attachment, Seq: rs.Seq, Snapshots: make([]json.RawMessage, 0, len(rs.Snapshots))}
	var reports []report
	for _, s := range rs.Snapshots {
		_, snapshot, why, ok := carried(rs.Attachment, rs.Seq, s)
		if !ok {
			continue
		}
		data.Snapshots = append(data.Snapshots, snapshot)
		if why != nil {
			reports = append(reports, report{s.Plugin, s.Mount, *why})
		}
	}
	a.tell(reports)
	return data
}

// end ends the attachment: nothing more is delivered or written for it, and its intents in flight settle stale.
func (a *attachment) end() {
	a.c.mu.Lock()
	a.ended = true
	a.queue, a.reports = nil, nil
	a.wake.Broadcast()
	a.c.mu.Unlock()
	a.t.Detach()
	a.stop()
}
