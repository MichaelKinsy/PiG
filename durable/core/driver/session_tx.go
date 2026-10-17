// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// txProxy is durable.Tx over one extension transaction (ABI section 8): every method is one request on the channel. The core
// enforces ReadAfterWrite and every other rule of spec section 4 and answers each operation, so the proxy forwards and never
// decides. Document drafts are Chord trackers over the value the core returns; their operations go back before the commit ends.
type txProxy struct {
	ctx  context.Context
	tx   *Tx
	docs map[string]*openDoc
	// order lists docs by acquisition: flush writes them in that order, as Pi's Map of drafts iterates (transaction.ts), not in Go's map order.
	order []string
	// retiring holds the drafts of retired incarnations: Pi persists a retired draft's final content (transaction.ts retireDoc: "Retirement of an
	// acquired draft persists its final content before retirement"), so flush writes them, first, naming the incarnation.
	retiring []*openDoc
}

type openDoc struct {
	address durable.DocumentAddress
	def     *durable.AnyDocDefinition
	tracker *delta.Tracker
	change  *delta.Change
	// draft is the one object tx.Doc returns for the document: one draft per address (Pi's memoized draft; the object is
	// compared by identity).
	draft *delta.Object
	// entry is the transaction's index for the document, deltasSinceBase the deltas stored after its newest base; both come from the
	// core's answer, and nil/-1 mean the core did not send one (an absent document has no deltas).
	entry           *int
	deltasSinceBase *int
	// migrated is set when this transaction migrated the stored value: the required base resets the count and checkpointWhen is not asked.
	migrated bool
}

func newTxProxy(ctx context.Context, tx *Tx) *txProxy {
	return &txProxy{ctx: ctx, tx: tx, docs: map[string]*openDoc{}}
}

func (p *txProxy) op(name string, args map[string]any) (json.RawMessage, error) {
	return p.tx.Op(p.ctx, name, args)
}

// decodeOptional decodes a result that is JSON null when the record is absent.
func decodeOptional[T any](raw json.RawMessage, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func decode[T any](raw json.RawMessage, err error) (T, error) {
	var out T
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func decodePage[T any](raw json.RawMessage, err error) (durable.Page[T, durable.Cursor], error) {
	var page durable.Page[T, durable.Cursor]
	if err != nil {
		return page, err
	}
	var w struct {
		Items []T                          `json:"items"`
		Next  map[string]durable.JsonValue `json:"next"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return page, err
	}
	page.Items = w.Items
	if w.Next != nil {
		next := durable.Cursor(w.Next)
		page.Next = &next
	}
	return page, nil
}

func (p *txProxy) Conversation(id durable.ConversationId) (*durable.ConversationRecord, error) {
	return decodeOptional[durable.ConversationRecord](p.op("conversation", map[string]any{"id": id}))
}

func (p *txProxy) Entry(id durable.EntryId) (*durable.EntryRecord, error) {
	return decodeOptional[durable.EntryRecord](p.op("entry", map[string]any{"id": id}))
}

func (p *txProxy) Task(id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	return decodeOptional[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]](p.op("task", map[string]any{"id": id}))
}

func (p *txProxy) ScanConversations(query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return decodePage[durable.ConversationRecord](p.op("scanConversations", map[string]any{"query": wireConversationQuery(query), "limit": limit, "cursor": cursor}))
}

func (p *txProxy) ScanEntries(query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return decodePage[durable.EntryRecord](p.op("scanEntries", map[string]any{"query": wireEntryQuery(query), "limit": limit, "cursor": cursor}))
}

func wireEntryQuery(q durable.EntryQuery) map[string]any {
	out := map[string]any{"conversationId": q.ConversationId}
	if q.MinEntryId != nil {
		out["minEntryId"] = *q.MinEntryId
	}
	if q.MaxEntryId != nil {
		out["maxEntryId"] = *q.MaxEntryId
	}
	if q.Order != nil {
		out["order"] = string(*q.Order)
	}
	return out
}

// wireConversationQuery is the query in the core's tx op shape (Pi's camelCase field names, absent filters omitted); the
// Go struct has no JSON names, so marshalling it as-is dropped every filter and a scan by owner returned every conversation.
func wireConversationQuery(q durable.ConversationQuery) map[string]any {
	out := map[string]any{}
	if q.OwnerConversationId != nil {
		out["ownerConversationId"] = *q.OwnerConversationId
	}
	if q.OwnerTaskId != nil {
		out["ownerTaskId"] = *q.OwnerTaskId
	}
	if q.Order != nil {
		out["order"] = string(*q.Order)
	}
	return out
}

// wireTaskQuery is the task query in the core's tx op shape, as wireConversationQuery.
func wireTaskQuery(q durable.TaskQuery) map[string]any {
	out := map[string]any{}
	if q.ConversationId != nil {
		out["conversationId"] = *q.ConversationId
	}
	if q.Kind != nil {
		out["kind"] = *q.Kind
	}
	if q.Status != nil {
		out["status"] = *q.Status
	}
	if q.AbortRequested != nil {
		out["abortRequested"] = *q.AbortRequested
	}
	if q.Background != nil {
		out["background"] = *q.Background
	}
	if q.Order != nil {
		out["order"] = string(*q.Order)
	}
	return out
}

func (p *txProxy) LatestHeadMarker(conversationId durable.ConversationId) (*durable.EntryRecord, error) {
	return decodeOptional[durable.EntryRecord](p.op("latestHeadMarker", map[string]any{"conversationId": conversationId}))
}

func (p *txProxy) ScanTasks(query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], durable.Cursor], error) {
	return decodePage[durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]](p.op("scanTasks", map[string]any{"query": wireTaskQuery(query), "limit": limit, "cursor": cursor}))
}

func (p *txProxy) SubmissionByRequest(conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	return decodeOptional[durable.SubmissionRecord](p.op("submissionByRequest", map[string]any{"conversationId": conversationId, "requestId": requestId}))
}

func (p *txProxy) CreateConversation(options durable.CreateConversationOptions) (durable.ConversationRecord, error) {
	return p.created(decode[durable.ConversationRecord](p.op("createConversation", map[string]any{"ownership": options.Ownership})))
}

// created runs HarnessOptions.ConversationCreated for a record this transaction created (the facade's TxProxy.created).
func (p *txProxy) created(record durable.ConversationRecord, err error) (durable.ConversationRecord, error) {
	if err != nil || p.tx.client.created == nil {
		return record, err
	}
	return record, p.tx.client.created(p, record)
}

// createRoot creates the reserved root conversation (PROTOCOL.md: createRootConversation).
func (p *txProxy) createRoot() (durable.ConversationRecord, error) {
	return p.created(decode[durable.ConversationRecord](p.op("createRootConversation", nil)))
}

func (p *txProxy) ForkConversation(parentConversationId durable.ConversationId, at durable.EntryId, options durable.CreateConversationOptions) (durable.ConversationRecord, error) {
	return p.created(decode[durable.ConversationRecord](p.op("forkConversation", map[string]any{"parentConversationId": parentConversationId, "at": at, "ownership": options.Ownership})))
}

// wireDraft is an entry draft as the core reads it: Head is an entry ID or "self".
func wireDraft(d durable.EntryDraft) map[string]any {
	out := map[string]any{"kind": d.Kind}
	if len(d.Model) > 0 {
		out["model"] = d.Model
	}
	if d.Data != nil {
		out["data"] = d.Data
	}
	if len(d.Edits) > 0 {
		out["edits"] = d.Edits
	}
	switch {
	case d.HeadSelf:
		out["head"] = "self"
	case d.Head != nil:
		out["head"] = *d.Head
	}
	return out
}

func (p *txProxy) AppendEntry(conversationId durable.ConversationId, value durable.EntryDraft) (durable.EntryRecord, error) {
	return decode[durable.EntryRecord](p.op("appendEntry", map[string]any{"conversationId": conversationId, "value": wireDraft(value)}))
}

func (p *txProxy) CreateTaskErased(task durable.AnyTask, input durable.JsonValue, options durable.TaskOptions) (durable.TaskId, error) {
	def := task.AnyDefinition()
	wire := map[string]any{"ownership": options.Ownership, "background": options.Background}
	if options.AbandonOnRestart != nil && *options.AbandonOnRestart {
		wire["abandonOnRestart"] = true
	}
	if options.ConversationId != nil {
		wire["conversationId"] = *options.ConversationId
	}
	body := map[string]any{"task": def.Name, "version": def.Version, "input": input, "options": wire}
	// The host computes initial(input) for a task whose definition it holds (PROTOCOL.md, createTask). The built-in tasks
	// are core code: the core builds their first checkpoint.
	if !builtinTask(def.Name) {
		var state durable.JsonValue
		err := callDefinition(func() (err error) { state, err = def.Initial(input); return err })
		if err != nil {
			return 0, err
		}
		body["state"] = state
	}
	return decode[durable.TaskId](p.op("createTask", body))
}

func (p *txProxy) CreateSubmission(create durable.SubmissionCreate) (durable.SubmissionRecord, error) {
	return decode[durable.SubmissionRecord](p.op("createSubmission", map[string]any{"create": create}))
}

func (p *txProxy) SettleSubmission(id durable.SubmissionId, settlement durable.SubmissionSettlement) error {
	_, err := p.op("settleSubmission", map[string]any{"id": id, "settlement": settlement})
	return err
}

func (p *txProxy) PlaceSubmission(id durable.SubmissionId, entry durable.EntryId) error {
	_, err := p.op("placeSubmission", map[string]any{"id": id, "entry": entry})
	return err
}

// wireAddress is the address form of the api and tx tables: {scope, definition, conversationId?, taskId?, key?}.
func wireAddress(a durable.DocumentAddress) map[string]any {
	out := map[string]any{"scope": string(a.Scope.Kind), "definition": a.Kind}
	switch a.Scope.Kind {
	case durable.ScopeConversation:
		out["conversationId"] = a.Scope.ConversationId
	case durable.ScopeTask:
		out["taskId"] = a.Scope.TaskId
	}
	if a.Key != nil {
		out["key"] = *a.Key
	}
	return out
}

// wireDef is a definition's identity and semantics as the core's docDef reads them: {kind, version, scope, history?, fork?, family?}.
func wireDef(def *durable.AnyDocDefinition) map[string]any {
	out := map[string]any{"kind": def.Kind, "version": def.Version, "scope": string(def.Scope)}
	if def.History != "" {
		out["history"] = string(def.History)
	}
	if def.Fork != "" {
		out["fork"] = string(def.Fork)
	}
	if def.Family {
		out["family"] = true
	}
	// migrates says the definition has a migrate function: the core asks the host to run it (migrateRead) instead of failing
	// "requires migration" (facade/tx.ts wireDefinition).
	if def.Migrate != nil {
		out["migrates"] = true
	}
	return out
}

// migrateRead runs one read-only document access and, when the core answers NeedMigrate {value, from}, runs the definition's
// migrate on the host and repeats the access with the result as `migrated` (facade/tx.ts migrateRead; the same exchange the tx
// doc op has as need "migrate").
func migrateRead[T any](def *durable.AnyDocDefinition, send func(migrated durable.JsonObject) (T, error)) (T, error) {
	out, err := send(nil)
	var remote *CoreError
	if err == nil || def.Migrate == nil || !errors.As(err, &remote) || remote.Name != "NeedMigrate" {
		return out, err
	}
	var need struct {
		Value durable.JsonObject `json:"value"`
		From  int                `json:"from"`
	}
	if jerr := json.Unmarshal([]byte(remote.Message), &need); jerr != nil {
		return out, jerr
	}
	migrated, merr := callMigrate(def, need.Value, need.From)
	if merr != nil {
		var zero T
		return zero, merr
	}
	return send(migrated)
}

func (p *txProxy) Doc(token durable.AnyDocToken, args ...any) (*delta.Object, error) {
	def := token.AnyDefinition()
	resolved, err := durable.ResolveAddress(def, args)
	if err != nil {
		return nil, err
	}
	if open, ok := p.docs[resolved.Id]; ok {
		return open.draft, nil
	}
	// The core checks a stored document record against the definition's semantics; without them it would assume
	// history "latest", so a rewindable document (pi.agent: rewindable, asOf) would not match its own record.
	wire := map[string]any{"address": wireAddress(resolved.Address), "version": def.Version, "def": wireDef(def)}
	if def.Family {
		if resolved.NextArgument < len(args) {
			wire["seed"] = args[resolved.NextArgument]
		}
	}
	// The core asks for the values only definition code can compute: initial(seed) for an absent document and migrate(value, from)
	// for an older stored version (PROTOCOL.md, `doc`; transaction.ts acquire). It asks at most twice, once for each.
	type docAnswer struct {
		Value           durable.JsonObject `json:"value"`
		Need            string             `json:"need"`
		From            int                `json:"from"`
		Entry           *int               `json:"entry"`
		DeltasSinceBase *int               `json:"deltasSinceBase"`
	}
	migratedHere := false
	answer, err := decode[docAnswer](p.op("doc", wire))
	for i := 0; err == nil && answer.Need != "" && i < 2; i++ {
		switch answer.Need {
		case "initial":
			var seed durable.JsonValue
			if def.Family && resolved.NextArgument < len(args) {
				seed = args[resolved.NextArgument]
			}
			initial, ierr := callInitial(def, seed)
			if ierr != nil {
				return nil, ierr
			}
			wire["initial"] = initial
		case "migrate":
			if def.Migrate == nil {
				return nil, fmt.Errorf("Document %s requires migration from version %d", def.Kind, answer.From)
			}
			migrated, merr := callMigrate(def, answer.Value, answer.From)
			if merr != nil {
				return nil, merr
			}
			wire["migrated"] = migrated
			migratedHere = true
		default:
			return nil, fmt.Errorf("the core asked for %q while opening document %s", answer.Need, def.Kind)
		}
		answer, err = decode[docAnswer](p.op("doc", wire))
	}
	if err != nil {
		return nil, err
	}
	if answer.Value == nil {
		answer.Value = delta.NewJsonObject(0)
	}
	tracker := delta.Track(answer.Value)
	change := tracker.BeginChange()
	p.order = append(p.order, resolved.Id)
	draft := change.State()
	p.docs[resolved.Id] = &openDoc{address: resolved.Address, def: def, tracker: tracker, change: change, draft: draft, entry: answer.Entry, deltasSinceBase: answer.DeltasSinceBase, migrated: migratedHere}
	return draft, nil
}

func (p *txProxy) RetireDoc(token durable.AnyDocToken, args ...any) error {
	resolved, err := durable.ResolveAddress(token.AnyDefinition(), args)
	if err != nil {
		return err
	}
	// The core checks the stored record against the definition's semantics, as for the doc op (facade/tx.ts retireDoc sends def).
	if _, err := p.op("retireDoc", map[string]any{"address": wireAddress(resolved.Address), "def": wireDef(token.AnyDefinition())}); err != nil {
		return err
	}
	if open, ok := p.docs[resolved.Id]; ok {
		p.retiring = append(p.retiring, open)
	}
	delete(p.docs, resolved.Id)
	p.order = slices.DeleteFunc(p.order, func(id string) bool { return id == resolved.Id })
	return nil
}

// flush sends every changed draft's operations before the commit ends: the retired incarnations' first, then the live drafts in
// acquisition order (facade/tx.ts flush).
func (p *txProxy) flush() error {
	retiring := p.retiring
	p.retiring = nil
	for _, open := range retiring {
		if err := p.writeDraft(open); err != nil {
			return err
		}
	}
	for _, id := range p.order {
		if err := p.writeDraft(p.docs[id]); err != nil {
			return err
		}
		delete(p.docs, id)
	}
	p.order = nil
	return nil
}

func (p *txProxy) writeDraft(open *openDoc) error {
	prepared, err := open.change.Prepare()
	if err != nil {
		return fmt.Errorf("document %s: %w", open.address.Kind, err)
	}
	defer prepared.Abort()
	if len(prepared.Ops()) == 0 {
		return nil
	}
	write := map[string]any{"address": wireAddress(open.address), "def": wireDef(open.def), "ops": prepared.Ops(), "baseRevision": prepared.BaseRevision()}
	if open.entry != nil {
		write["entry"] = *open.entry
	}
	// Pi runs checkpointWhen at settlement on the prepared value and ops (session/documents.ts); the driver has both here.
	if !open.migrated && open.deltasSinceBase != nil && open.def.CheckpointWhen != nil {
		checkpoint, err := callCheckpoint(open.def, prepared.Value(), prepared.Ops(), durable.CheckpointInfo{DeltasSinceBase: *open.deltasSinceBase})
		if err != nil {
			return err
		}
		if checkpoint {
			write["checkpoint"] = true
		}
	}
	_, err = p.op("doc.write", write)
	return err
}

func (p *txProxy) discard() {
	for _, open := range p.docs {
		open.change.Abort()
	}
	for _, open := range p.retiring {
		open.change.Abort()
	}
	p.docs = map[string]*openDoc{}
	p.order = nil
	p.retiring = nil
}

// runCommit runs change as one Session commit: begin (waiting for the line), callback, flush drafts, end. A failure aborts.
func runCommit(ctx context.Context, begin func(context.Context) (*Tx, error), change func(durable.Tx) (any, error)) (any, error) {
	tx, err := begin(ctx)
	if err != nil {
		return nil, err
	}
	proxy := newTxProxy(ctx, tx)
	result, err := change(proxy)
	if err == nil {
		err = proxy.flush()
	}
	if err != nil {
		proxy.discard()
		// The abort's own failure is not the caller's error.
		_ = tx.Abort(context.WithoutCancel(ctx), err)
		return nil, err
	}
	if err := tx.End(ctx, nil); err != nil {
		return nil, err
	}
	return result, nil
}

// builtinTask reports the tasks the core runs itself.
func builtinTask(name string) bool {
	return name == "generation" || name == "tool" || name == "compaction"
}

// callDefinition runs a definition callback. Upstream callbacks may throw, which rejects the operation; a typed Go callback that
// cannot return an error panics instead, and the panic becomes the operation's error so the commit rolls back exactly as on a
// throw (the in-process Session does the same, session/transaction.go callDefinition).
func callDefinition(run func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return run()
}

func callMigrate(def *durable.AnyDocDefinition, value durable.JsonObject, from int) (out durable.JsonObject, err error) {
	err = callDefinition(func() (err error) { out, err = def.Migrate(value, from); return err })
	return out, err
}

func callInitial(def *durable.AnyDocDefinition, seed durable.JsonValue) (out durable.JsonObject, err error) {
	err = callDefinition(func() (err error) { out, err = def.Initial(seed); return err })
	return out, err
}

func callCheckpoint(def *durable.AnyDocDefinition, value durable.JsonObject, ops []durable.Op, info durable.CheckpointInfo) (base bool, err error) {
	err = callDefinition(func() (err error) { base, err = def.CheckpointWhen(value, ops, info); return err })
	return base, err
}
