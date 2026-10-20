// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// hostTx is one extension transaction (ABI section 8): a Session transaction whose callback runs in the host, one
// operation per tx event, from begin to end or abort.
type hostTx struct {
	id    uint32
	scope session.TxScope
	tx    *session.Tx // nil until the transaction holds the Session line
	// rt is the invocation of a custom task phase that began the transaction; current is the task record read at begin.
	rt      *session.Runtime
	current *jv.Object
	docs    map[string]*hostDoc
	// mintDone is set when the host's Storage answered the mintId a mint operation asked for; the operation then runs and
	// clears it.
	mintDone bool
	// convDone is set when the host's Storage answered the conversation read an operation makes first (transaction.ts #requireConversation).
	convDone bool
	// elapsed is the microtask passes the host's callback spent holding the line (the end operation's `elapsed`): Pi's job duration
	// includes them, so the commit is held for the rest.
	elapsed int
	// abandoned is set when the host aborted the transaction before it held the line (a caller that stopped waiting for it): the
	// transaction still takes its turn in the line's order, which grant then gives up at once.
	abandoned bool
}

// hostDoc is the definition of a document a host transaction opened, with the values the host computed for it.
type hostDoc struct {
	def        *session.DocDef
	args       session.DocArgs
	initial    *jv.Object
	hasInitial bool
	migrated   *jv.Object
	hasMigrate bool
	checkpoint bool
	// found is set when the host's Storage answered the findDocument the first access to the document asked for.
	found bool
}

// needHost is the error a host-run definition function returns when the host has not sent its value yet.
func needHost(kind string) error {
	return &session.NeedRead{Reads: []session.ReadRequest{{Kind: kind}}}
}

func (h *handler) txAnswer(id uint32, body []byte) { h.notice(abi.NoticeTxResult, id, body) }

func (h *handler) txResult(id uint32, value any) {
	out := append(make([]byte, 0, 64), `{"result":`...)
	out = jv.Append(out, value)
	h.txAnswer(id, append(out, '}'))
}

func (h *handler) txRejected(id uint32, err error) {
	out := append(make([]byte, 0, 64), `{"rejected":`...)
	out = append(out, errorJSON(err)...)
	h.txAnswer(id, append(out, '}'))
}

//go:noinline
func (h *handler) onTx(ev *abi.Event) error {
	v, err := jv.Parse(ev.Payload)
	if err != nil {
		return err
	}
	op, ok := v.(*jv.Object)
	if !ok {
		return &Rejection{"TypeError", "tx: the operation is not an object"}
	}
	name := strOf(op, "op")
	if name == "begin" {
		return h.txBegin(ev.ID, op)
	}
	t := h.txs[ev.ID]
	if t != nil && t.tx == nil && name == "abort" {
		// The host stopped waiting for the line: no callback will run for this transaction, so it must never hold the line (a
		// holder nobody ends blocks every later job and the close). Its turn in the queue stays, as a job whose caller gave up does.
		t.abandoned = true
		return nil
	}
	if t == nil || t.tx == nil {
		return &Rejection{"Error", "the transaction is not open"}
	}
	switch name {
	case "end":
		t.elapsed = int(numOf(op, "elapsed"))
		return h.txEnd(t, objOf(op, "state"))
	case "abort":
		e := objOf(op, "error")
		if t.rt != nil {
			t.rt.AbandonHost(t.tx, errors.New(strOf(e, "message")))
		} else {
			h.sess.Abandon(t.tx, errors.New(strOf(e, "message")))
		}
		h.release(t)
		return nil
	}
	h.txRun(t, name, op)
	return nil
}

// Read-only: assigned once at initialization.
//
// errTxWaiting is what a transaction operation returns while the host's Storage answers a read it asked for; the
// operation runs again from the start, and answers, when the read completes.
var errTxWaiting = errors.New("transaction operation waiting for the host's Storage")

// txRun runs one operation and answers it, unless it waits for the host's Storage.
func (h *handler) txRun(t *hostTx, name string, op *jv.Object) {
	// A transaction operation asks the Storage for its reads itself (txStorageRead, before it changes anything): the adapter's
	// shadow reads (gateRead) stay out of it.
	h.inTxOp = true
	result, err := h.txOp(t, name, op)
	h.inTxOp = false
	if errors.Is(err, errTxWaiting) {
		return
	}
	t.mintDone, t.convDone = false, false
	if err != nil {
		if need, ok := session.IsNeedRead(err); ok && len(need.Reads) != 0 {
			h.txResultNeed(t.id, need.Reads[0].Kind, result)
			return
		}
		h.txRejected(t.id, err)
		return
	}
	h.txResult(t.id, result)
}

// txStorageRead asks the host's Storage to run one Pi Storage call (hook storageRead) that the operation makes before it
// takes effect: session.ts #acquire loads the document with findDocument, and every record ID is minted with mintId. The
// operation answers when the call completes, as the transaction callback's await does; a rejection answers the operation.
func (h *handler) txStorageRead(t *hostTx, name string, op *jv.Object, method string, args any, done func()) {
	body := jv.NewObject()
	body.Set("name", "storageRead")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", jobj("method", method, "args", args))
	h.startEffect(abi.EffectHook, jv.Append(nil, body), &pending{done: func(outcome uint8, payload []byte) {
		if outcome != abi.OutcomeResult {
			value, err := jv.Parse(payload)
			if err != nil {
				h.txRejected(t.id, err)
			} else {
				h.txRejected(t.id, thrown(value))
			}
			return
		}
		done()
		h.txRun(t, name, op)
	}})
}

// requiredConversation is the conversation an operation requires to exist, or 0 when it names none (the operation then reports its own
// error).
func requiredConversation(t *hostTx, name string, op *jv.Object) session.ID {
	switch name {
	case "appendEntry":
		return session.ID(numOf(op, "conversationId"))
	case "createTask":
		// options.conversationId, else the conversation the commit is bound to (transaction.ts createTask defaults).
		if id := session.ID(numOf(objOf(op, "options"), "conversationId")); id != 0 {
			return id
		}
		return t.scope.ConversationID
	case "createSubmission":
		return session.ID(numOf(objOf(op, "create"), "conversationId"))
	}
	return 0
}

// mintOps are the operations that mint one record ID through the Storage (transaction.ts createConversation, appendEntry,
// createTask, createSubmission).
func isMintOp(name string) bool {
	switch name {
	case "createRootConversation", "createConversation", "appendEntry", "createTask", "createSubmission":
		return true
	}
	return false
}

// txResultNeed answers an operation that needs a value only the host's definition code can compute: {result: {need,
// ...}}.
func (h *handler) txResultNeed(id uint32, kind string, detail any) {
	out := append(make([]byte, 0, 64), `{"result":{"need":`...)
	out = jv.AppendString(out, kind)
	if o, ok := detail.(*jv.Object); ok {
		o.Entries(func(k string, v any) bool {
			out = append(out, ',')
			out = jv.AppendString(out, k)
			out = append(out, ':')
			out = jv.Append(out, v)
			return true
		})
	}
	h.txAnswer(id, append(out, '}', '}'))
}

func (h *handler) txBegin(id uint32, op *jv.Object) error {
	if _, dup := h.txs[id]; dup {
		return &Rejection{"Error", "the transaction is already open"}
	}
	// A transaction is commitWith: its admission is checked as it is enqueued (session.ts #assertUsable).
	if err := h.sess.Admit(); err != nil {
		h.txRejected(id, err)
		return nil
	}
	t := &hostTx{id: id, docs: map[string]*hostDoc{}}
	t.scope.ConversationID = session.ID(numOf(op, "conversationId"))
	if invocation := session.ID(numOf(op, "invocation")); invocation != 0 {
		if t.rt = h.runtimes[invocation]; t.rt == nil {
			return &Rejection{"Error", "transaction begin: no running invocation of task " + itoa(int64(invocation))}
		}
	}
	h.txs[id] = t
	if h.loop != nil {
		// The transaction takes its turn on the Session line behind the jobs already queued (session.ts #enqueue is one FIFO).
		if h.holder != 0 || h.loop.Busy() {
			h.txAnswer(id, []byte(`{"waiting":true}`))
		}
		h.loop.LineBare(func() error { return h.grant(t) })
		return nil
	}
	if h.holder != 0 {
		h.queue = append(h.queue, id)
		h.txAnswer(id, []byte(`{"waiting":true}`))
		return nil
	}
	return h.grant(t)
}

// grant gives the Session line to a transaction and tells the host it is ready. A transaction the host abandoned while it waited
// takes nothing.
func (h *handler) grant(t *hostTx) error {
	if t.abandoned {
		delete(h.txs, t.id)
		return nil
	}
	var tx *session.Tx
	var err error
	if t.rt != nil {
		tx, t.current, err = t.rt.BeginHost()
	} else {
		tx, err = h.sess.Begin(t.scope)
	}
	if err != nil {
		delete(h.txs, t.id)
		h.txRejected(t.id, err)
		return nil
	}
	t.tx = tx
	h.holder = t.id
	if t.current != nil {
		// A transaction of a task invocation also answers the task record it read as it took the line: the `current` of
		// Runtime.commit's callback.
		h.txAnswer(t.id, append(append([]byte(`{"ready":true,"current":`), jv.Append(nil, t.current)...), '}'))
		return nil
	}
	h.txAnswer(t.id, []byte(`{"ready":true}`))
	return nil
}

// release frees the line and grants it to the next waiting transaction.
func (h *handler) release(t *hostTx) {
	delete(h.txs, t.id)
	if h.holder == t.id {
		h.holder = 0
	}
	if h.loop != nil {
		// The Loop starts the next job, a transaction's turn included, once the line is free.
		h.loop.Kick()
		return
	}
	for h.holder == 0 && len(h.queue) != 0 {
		next := h.txs[h.queue[0]]
		h.queue = h.queue[1:]
		if next != nil {
			_ = h.grant(next)
		}
	}
}

func (h *handler) txEnd(t *hostTx, state *jv.Object) error {
	held := h.loop != nil
	if held {
		h.sess.HoldPublications()
	}
	var err error
	if t.rt != nil {
		_, err = t.rt.FinishHost(t.tx, t.current, state)
	} else {
		_, err = h.sess.Finish(t.tx)
	}
	var pubs []*session.Publication
	if held {
		pubs = h.sess.HeldPublications()
	}
	if session.IsNeedGate(err) {
		// The host's Storage holds the commit: the transaction keeps the line, and its end is answered when the Storage
		// accepts or refuses the writes (gate.go).
		h.cg.resume = func() { h.txEndSettled(t) }
		h.txAnswer(t.id, []byte(`{"gated":true}`))
		return nil
	}
	if err != nil {
		// A refused commit ends no held job: its Transaction ticks (Session.Finish) must not become the next line job's change.
		h.sess.TakeTicks()
		h.release(t)
		return &Rejection{errorName(err), errorMessage(err)}
	}
	h.txCommitted(t, pubs)
	return nil
}

// txEndSettled commits the transaction whose commit the host's Storage held, and answers its end.
func (h *handler) txEndSettled(t *hostTx) {
	held := h.loop != nil
	if held {
		h.sess.HoldPublications()
	}
	var err error
	if t.rt != nil {
		_, err = t.rt.FinishHostSettled(t.tx)
	} else {
		_, err = h.sess.FinishSettled(t.tx)
	}
	var pubs []*session.Publication
	if held {
		pubs = h.sess.HeldPublications()
	}
	if err != nil {
		h.sess.TakeTicks()
		h.release(t)
		h.txRejected(t.id, err)
		return
	}
	h.txCommitted(t, pubs)
}

// txCommitted ends a committed transaction. The commit resolves at the end of Pi's job (the change's awaits, settle,
// storage, #runCommit's return): in Harness mode its publication, the release of the line and the {result: null} answer
// that resolves the host's end() wait for that duration (ABI section 5.3), as for the core's own line jobs.
func (h *handler) txCommitted(t *hostTx, pubs []*session.Publication) {
	t.tx = nil
	total := max(h.jobHops()-t.elapsed, 0)
	h.job = jobStats{mintStart: h.sess.NextID()}
	answer := func() { h.txResult(t.id, nil) }
	publish := func() {
		for _, pub := range pubs {
			h.sess.Publish(pub)
		}
	}
	end := func() { h.release(t) }
	if h.loop == nil {
		publish()
		end()
		answer()
		return
	}
	s := hostCommitSchedule(total)
	steps := []holdStep{{s.publish, publish}, {s.answer, answer}}
	if !h.holdSteps(s.release, steps, end) {
		end()
	}
}

// hostCommitSchedule is when, in hops after the transaction's last host operation, the core publishes a host-driven commit, answers its end and frees the
// line, for a Pi job whose caller resumes total hops later (jobHops less the passes the host's callback already spent). Pi's job frees the line one hop
// before its caller resumes, so the next line job starts with the caller, and publishes pubBeforeEnd hops before it frees the line, as a core line job
// does (session.ts #runCommit :548-549 under #enqueue :647-654; stamped in Pi ea448f45 on F-ticks-node's root create: publication 137, root()'s caller
// and the sweep job 140). The facade needs hostAnswerTicks passes from the core's answer to its caller, so the answer goes out that much earlier.
// F-ticks-{node,gate} (cold submit 89/103) and F-ticks-runtime-drift-{node,gate} (waitForTask on a live task 109/119) bind the publication and release.
type hostCommitHops struct{ publish, answer, release int }

func hostCommitSchedule(total int) hostCommitHops {
	release := max(total-1, 0)
	return hostCommitHops{publish: max(release-pubBeforeEnd, 0), answer: max(total-hostAnswerTicks, 0), release: release}
}

// hostAnswerTicks is the microtask passes the facade spends from the core's tx_result to its caller's resumption: end()'s one reaction and the adoption of
// its promise by runCommit's return, which the caller then awaits (host/cf/src/client.ts end, facade/tx.ts runCommit).
const hostAnswerTicks = 3

// publish emits the published notice of one commit (durable/core/host/PROTOCOL.md, Notices). The notice is written an
// item at a time into abi.Chunks, so a 20,000-task commit's (megabytes) needs no block of its size.
func (h *handler) publish(pub *session.Publication) {
	var out abi.Chunks
	item := append(make([]byte, 0, 128), `{"seq":`...)
	out.Append(jv.AppendNumber(item, float64(pub.Seq)))
	for _, kind := range [...]session.WriteKind{session.WriteSubmission, session.WriteEntry, session.WriteTask} {
		item = append(item[:0], `,"`...)
		switch kind {
		case session.WriteSubmission:
			item = append(item, "submissions"...)
		case session.WriteEntry:
			item = append(item, "entries"...)
		default:
			item = append(item, "tasks"...)
		}
		out.Append(append(item, `":[`...))
		first := true
		for _, c := range pub.Changes {
			if c.Kind != session.ChangeTable || c.Table != kind {
				continue
			}
			item = item[:0]
			if !first {
				item = append(item, ',')
			}
			first = false
			switch kind {
			case session.WriteEntry:
				item = jv.AppendNumber(item, numOf(c.Record, "id"))
			case session.WriteTask:
				item = append(item, `{"id":`...)
				item = jv.AppendNumber(item, numOf(c.Record, "id"))
				item = append(item, `,"kind":`...)
				item = jv.AppendString(item, strOf(c.Record, "kind"))
				item = append(item, `,"status":`...)
				item = jv.AppendString(item, strOf(objOf(c.Record, "state"), "status"))
				item = append(item, '}')
			case session.WriteSubmission:
				item = append(item, `{"id":`...)
				item = jv.AppendNumber(item, numOf(c.Record, "id"))
				item = append(item, `,"conversationId":`...)
				item = jv.AppendNumber(item, numOf(c.Record, "conversationId"))
				if r, ok := c.Record.Get("requestId"); ok {
					item = append(item, `,"requestId":`...)
					item = jv.Append(item, r)
				}
				item = append(item, `,"status":`...)
				item = jv.AppendString(item, strOf(c.Record, "status"))
				item = append(item, '}')
			}
			out.Append(item)
		}
		out.Append(append(item[:0], ']'))
	}
	if h.changes {
		out.Append(jv.Append(append(item[:0], `,"changes":`...), commitChanges(pub)))
	}
	out.Append(append(item[:0], '}'))
	h.b.NoticeChunks(abi.NoticePublished, out)
	h.publishWatches(pub)
}

// page renders Pi's Page<T, Cursor>: {items, next?: {after, order}}.
func page(p session.Page, order string) *jv.Object {
	items := make([]any, len(p.Items))
	for i, it := range p.Items {
		items[i] = it
	}
	out := jv.NewObject()
	out.Set("items", items)
	if p.After != 0 {
		next := jv.NewObject()
		next.Set("after", float64(p.After))
		next.Set("order", order)
		out.Set("next", next)
	}
	return out
}

// rawPage is page for items that are already JSON text, spliced into the answer as they are.
func rawPage(items []jv.Raw, after session.ID, order string) *jv.Object {
	list := make([]any, len(items))
	for i, it := range items {
		list[i] = it
	}
	out := jv.NewObject()
	out.Set("items", list)
	if after != 0 {
		next := jv.NewObject()
		next.Set("after", float64(after))
		next.Set("order", order)
		out.Set("next", next)
	}
	return out
}

// scanStart is pi-durable's scanStart (storage/scan.ts): the order a scan runs in and the ID it continues after.
func scanStart(query *jv.Object, cursor any, fallback string) (order string, after session.ID, err error) {
	requested, hasOrder := "", false
	if v, ok := query.Get("order"); ok && v != nil {
		s, isStr := v.(string)
		if !isStr || (s != "ascending" && s != "descending") {
			return "", 0, session.NewStorageRequestError("Invalid scan order: " + jsString(v))
		}
		requested, hasOrder = s, true
	}
	c, _ := cursor.(*jv.Object)
	if c == nil {
		if hasOrder {
			return requested, 0, nil
		}
		return fallback, 0, nil
	}
	a, ok := c.Get("after")
	f, isNum := a.(float64)
	if !ok || !isNum || f != float64(int64(f)) {
		return "", 0, session.NewStorageRequestError("Invalid storage cursor")
	}
	order = fallback
	if sv, ok := c.Get("order"); ok {
		s, isStr := sv.(string)
		if !isStr || (s != "ascending" && s != "descending") {
			return "", 0, session.NewStorageRequestError("Invalid storage cursor")
		}
		order = s
	}
	if hasOrder && requested != order {
		return "", 0, session.NewStorageRequestError("The cursor continues a " + order + " scan; the query asks for " + requested)
	}
	return order, session.ID(f), nil
}

func jsString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jv.Stringify(v)
}

func optBool(o *jv.Object, k string) *bool {
	if b, ok := boolOf(o, k); ok {
		return &b
	}
	return nil
}

// txOp runs one Tx operation. A nil record answers null.
func (h *handler) txOp(t *hostTx, name string, op *jv.Object) (any, error) {
	tx := t.tx
	// transaction.ts #requireConversation: appendEntry, createTask and createSubmission read the conversation from the Storage first,
	// unless this transaction created it.
	if conv := requiredConversation(t, name, op); conv != 0 && !t.convDone && h.gated("conversation") && !tx.ConversationCreated(conv) {
		h.txStorageRead(t, name, op, "conversation", []any{float64(conv)}, func() { t.convDone = true })
		return nil, errTxWaiting
	}
	if isMintOp(name) && !t.mintDone && h.gated("mintId") {
		h.txStorageRead(t, name, op, "mintId", jv.NewObject(), func() { t.mintDone = true })
		return nil, errTxWaiting
	}
	// transaction.ts conversation, entry and task read the record from the Storage (storage.conversation, entry, task) before they answer.
	if (name == "conversation" || name == "entry" || name == "task") && !t.mintDone && h.gated(name) {
		h.txStorageRead(t, name, op, name, []any{numOf(op, "id")}, func() { t.mintDone = true })
		return nil, errTxWaiting
	}
	id := session.ID(numOf(op, "id"))
	nilRec := func(o *jv.Object, err error) (any, error) {
		if err != nil || o == nil {
			return nil, err
		}
		return o, nil
	}
	switch name {
	case "conversation":
		return nilRec(tx.Conversation(id))
	case "entry":
		e, err := tx.Entry(id)
		if err != nil || e == nil {
			return nil, err
		}
		if kind := strOf(op, "kind"); has(op, "kind") && strOf(e, "kind") != kind {
			return nil, nil
		}
		return e, nil
	case "task":
		return nilRec(tx.Task(id))
	case "latestHeadMarker":
		return nilRec(tx.LatestHeadMarker(session.ID(numOf(op, "conversationId"))))
	case "submissionByRequest":
		return nilRec(tx.SubmissionByRequest(session.ID(numOf(op, "conversationId")), strOf(op, "requestId")))
	case "scanConversations", "scanEntries", "scanTasks":
		return h.scan(tx, name, op)
	case "createRootConversation":
		return tx.CreateRootConversation()
	case "createConversation":
		return tx.CreateConversation(ownership(objOf(op, "ownership")))
	case "forkConversation":
		return tx.ForkConversation(session.ID(numOf(op, "parentConversationId")), session.ID(numOf(op, "at")), ownership(objOf(op, "ownership")))
	case "appendEntry":
		draft := objOf(op, "value")
		if draft == nil {
			return nil, &typeError{"Tx.appendEntry() requires an entry draft"}
		}
		if kind, ok := op.Get("kind"); ok {
			draft = draft.Shallow()
			draft.Set("kind", kind)
		}
		return tx.AppendEntry(session.ID(numOf(op, "conversationId")), draft)
	case "createTask":
		return h.createTask(tx, op)
	case "setTask":
		value := objOf(op, "value")
		if value == nil {
			return nil, &typeError{"Tx.setTask() requires a task record"}
		}
		return nil, tx.SetTask(value)
	case "createCompaction":
		id, err := turn.CreateCompaction(tx, session.ID(numOf(op, "conversationId")), objOf(op, "input"), 0)
		return float64(id), err
	case "createSubmission":
		return tx.CreateSubmission(objOf(op, "create"))
	case "settleSubmission":
		return nil, tx.SettleSubmission(id, objOf(op, "settlement"))
	case "placeSubmission":
		return nil, tx.PlaceSubmission(id, session.ID(numOf(op, "entry")))
	case "doc":
		return h.txDoc(t, op)
	case "doc.write":
		return h.txDocWrite(t, op)
	case "retireDoc":
		def, args, err := docDef(op)
		if err != nil {
			return nil, err
		}
		return nil, tx.RetireDoc(def, args)
	}
	return nil, &Rejection{"Error", "unknown transaction operation " + name}
}

func ownership(o *jv.Object) session.Ownership {
	if strOf(o, "kind") == "task" {
		return session.Ownership{Task: session.ID(numOf(o, "taskId"))}
	}
	return session.Ownership{}
}

func (h *handler) scan(tx *session.Tx, name string, op *jv.Object) (any, error) {
	q := objOf(op, "query")
	if q == nil {
		q = jv.NewObject()
	}
	cursor, _ := op.Get("cursor")
	limit := int(numOf(op, "limit"))
	switch name {
	case "scanConversations":
		order, after, err := scanStart(q, cursor, "ascending")
		if err != nil {
			return nil, err
		}
		p, err := tx.ScanConversations(session.ConversationQuery{
			OwnerConversationID: session.ID(numOf(q, "ownerConversationId")), OwnerTaskID: session.ID(numOf(q, "ownerTaskId")),
			Descending: order == "descending",
		}, limit, after)
		if err != nil {
			return nil, err
		}
		return page(p, order), nil
	case "scanEntries":
		order, after, err := scanStart(q, cursor, "descending")
		if err != nil {
			return nil, err
		}
		p, err := tx.ScanEntries(session.EntryQuery{
			ConversationID: session.ID(numOf(q, "conversationId")), MinEntryID: session.ID(numOf(q, "minEntryId")),
			MaxEntryID: session.ID(numOf(q, "maxEntryId")), Ascending: order == "ascending",
		}, limit, after)
		if err != nil {
			return nil, err
		}
		return page(p, order), nil
	default:
		order, after, err := scanStart(q, cursor, "ascending")
		if err != nil {
			return nil, err
		}
		p, err := tx.ScanTasks(session.TaskQuery{
			ConversationID: session.ID(numOf(q, "conversationId")), Kind: strOf(q, "kind"), Status: strOf(q, "status"),
			AbortRequested: optBool(q, "abortRequested"), Background: optBool(q, "background"), Descending: order == "descending",
		}, limit, after)
		if err != nil {
			return nil, err
		}
		return page(p, order), nil
	}
}

// createTask is Tx.createTask: the host computed initial(input) for a task whose definition it holds and sends it as
// state; a built-in task (generation, tool, compaction) gets its first checkpoint from the core.
func (h *handler) createTask(tx *session.Tx, op *jv.Object) (any, error) {
	name := strOf(op, "task")
	version := int(numOf(op, "version"))
	if version == 0 {
		version = 1
	}
	state, hasState := op.Get("state")
	if !hasState {
		for _, impl := range h.builtins {
			if impl.Def.Name == name {
				// The record stores the version the caller's definition names (a task stored by a newer definition).
				def := impl.Def
				def.Version = version
				return h.createTaskWith(tx, &def, op)
			}
		}
	}
	def := &session.TaskDef{Name: name, Version: version, Initial: func(any) any {
		if hasState {
			return state
		}
		return nil
	}}
	return h.createTaskWith(tx, def, op)
}

func (h *handler) createTaskWith(tx *session.Tx, def *session.TaskDef, op *jv.Object) (any, error) {
	opts := objOf(op, "options")
	own := objOf(opts, "ownership")
	if own == nil {
		return nil, &typeError{"Tx.createTask() requires options.ownership"}
	}
	var to session.TaskOptions
	if strOf(own, "kind") == "task" {
		to.OwnerTask = session.ID(numOf(own, "taskId"))
	}
	to.ConversationID = session.ID(numOf(opts, "conversationId"))
	to.Background, _ = boolOf(opts, "background")
	to.AbandonOnRestart, _ = boolOf(opts, "abandonOnRestart")
	input, _ := op.Get("input")
	id, err := tx.CreateTask(def, input, to)
	if err != nil {
		return nil, err
	}
	return float64(id), nil
}

// docDef reads a document address and definition from an operation: address {scope, definition, conversationId?,
// taskId?, key?} and def {kind, version, scope, history?, fork?, family?}.
func docDef(op *jv.Object) (*session.DocDef, session.DocArgs, error) {
	addr := objOf(op, "address")
	if addr == nil {
		addr = objOf(op, "doc")
	}
	if addr == nil {
		return nil, session.DocArgs{}, &typeError{"a document operation requires an address"}
	}
	d := objOf(op, "def")
	def := &session.DocDef{Kind: strOf(addr, "definition"), Scope: session.ScopeKind(strOf(addr, "scope"))}
	if d != nil {
		if k := strOf(d, "kind"); k != "" {
			def.Kind = k
		}
		def.Version = int(numOf(d, "version"))
		def.History = session.History(strOf(d, "history"))
		def.Fork = session.ForkPolicy(strOf(d, "fork"))
		def.Family, _ = boolOf(d, "family")
	}
	if def.Version == 0 {
		def.Version = int(numOf(op, "version"))
	}
	if def.Version == 0 {
		def.Version = 1
	}
	if def.Scope == session.ScopeConversation && def.History == "" {
		def.History = "latest"
	}
	if def.Scope == session.ScopeConversation && def.Fork == "" {
		def.Fork = "copy"
	}
	var args session.DocArgs
	switch def.Scope {
	case session.ScopeConversation:
		args.Owner = session.ID(numOf(addr, "conversationId"))
	case session.ScopeTask:
		args.Owner = session.ID(numOf(addr, "taskId"))
	}
	if k, ok := addr.Get("key"); ok {
		s, _ := k.(string)
		args.Key = s
		def.Family = true
	}
	if seed, ok := op.Get("seed"); ok {
		args.Seed = seed
	}
	return def, args, nil
}

// txDoc is Tx.doc: the document's draft starts from the returned value; the host keeps the draft and sends its
// operations with doc.write. The host runs initial and migrate: the first answer may be {need: "initial"} or {need:
// "migrate", value, from}, and the operation comes again with initial or migrated.
func (h *handler) txDoc(t *hostTx, op *jv.Object) (any, error) {
	def, args, err := docDef(op)
	if err != nil {
		return nil, err
	}
	key := session.DocAddress(def, args)
	hd := t.docs[key]
	if hd == nil {
		hd = &hostDoc{def: def, args: args}
		t.docs[key] = hd
	}
	if v, ok := op.Get("initial"); ok {
		hd.initial, hd.hasInitial = asObject(v), true
	}
	if v, ok := op.Get("migrated"); ok {
		hd.migrated, hd.hasMigrate = asObject(v), true
	}
	// session.ts #acquire: a document that is not in the Session's loaded cache is looked up in the Storage (findDocument) first.
	if !hd.found && !hd.hasInitial && !hd.hasMigrate && h.gated("findDocument") && !h.sess.Loaded(def, args) {
		h.txStorageRead(t, "doc", op, "findDocument", jobj("address", docAddress(op)), func() { hd.found = true })
		return nil, errTxWaiting
	}
	waitMint := false
	def.HostInitial = func(any) (*jv.Object, error) {
		if !hd.hasInitial {
			return nil, needHost("initial")
		}
		// After initial(), an absent document takes its ID from Storage.mintId.
		if !t.mintDone && h.gated("mintId") {
			// A NeedRead leaves no document entry behind: the call runs again once the Storage answered.
			waitMint = true
			return nil, needHost("mint")
		}
		return hd.initial, nil
	}
	var migrateFrom *jv.Object
	def.HostMigrate = func(value *jv.Object, from int) (*jv.Object, error) {
		if !hd.hasMigrate {
			migrateFrom = jv.NewObject()
			migrateFrom.Set("value", value)
			migrateFrom.Set("from", float64(from))
			return nil, needHost("migrate")
		}
		return hd.migrated, nil
	}
	def.CheckpointWhen = func(*jv.Object, []delta.Op, int) bool { return hd.checkpoint }
	hd.def = def
	value, deltas, loaded, err := t.tx.HostDoc(def, args)
	if waitMint {
		h.txStorageRead(t, "doc", op, "mintId", jv.NewObject(), func() { t.mintDone = true })
		return nil, errTxWaiting
	}
	if err != nil {
		if migrateFrom != nil {
			return migrateFrom, err
		}
		return nil, err
	}
	out := jv.NewObject()
	out.Set("value", value)
	out.Set("entry", float64(t.tx.DocEntry(def, args)))
	if loaded {
		out.Set("id", float64(t.tx.DocRecordID(def, args)))
		out.Set("deltasSinceBase", float64(deltas))
	}
	return out, nil
}

// txDocWrite takes a draft's operations (Chord Op[]) and the host's checkpointWhen verdict.
func (h *handler) txDocWrite(t *hostTx, op *jv.Object) (any, error) {
	def, args, err := docDef(op)
	if err != nil {
		return nil, err
	}
	hd := t.docs[session.DocAddress(def, args)]
	if hd == nil {
		return nil, &Rejection{"Error", "doc.write for a document the transaction did not open"}
	}
	raw, _ := op.Get("ops")
	ops, err := delta.DecodeOps(raw)
	if err != nil {
		return nil, err
	}
	hd.checkpoint, _ = boolOf(op, "checkpoint")
	entry := -1
	if has(op, "entry") {
		entry = int(numOf(op, "entry"))
	}
	return nil, t.tx.SetDocOps(hd.def, hd.args, entry, ops)
}

// docAddress is the Storage's DocumentAddress ({kind, scope, key?}) of the wire address of a document operation.
func docAddress(op *jv.Object) *jv.Object {
	addr := objOf(op, "address")
	scope := jobj("kind", strOf(addr, "scope"))
	if has(addr, "conversationId") {
		scope.Set("conversationId", numOf(addr, "conversationId"))
	}
	if has(addr, "taskId") {
		scope.Set("taskId", numOf(addr, "taskId"))
	}
	out := jobj("kind", strOf(addr, "definition"), "scope", scope)
	if k, ok := addr.Get("key"); ok {
		out.Set("key", k)
	}
	return out
}

func asObject(v any) *jv.Object {
	o, _ := v.(*jv.Object)
	return o
}

// commitChanges renders the CommitPublication changes of Session.subscribeCommits (types.ts CommitChange): a table
// write is {type, value: record}; a document change {type: "document", record, conversationId, version, value, ops}
// with null value and version absent for a retirement; a copy {type: "document.copy", record, conversationId, source}.
//
// Not inlined: only a commit listener asks for changes, and an inlined copy would be compiled by V8 with its hot caller, publish.
//
//go:noinline
func commitChanges(pub *session.Publication) []any {
	out := make([]any, 0, len(pub.Changes))
	for _, c := range pub.Changes {
		o := jv.NewObject()
		switch c.Kind {
		case session.ChangeTable:
			o.Set("type", [...]string{session.WriteConversation: "conversation", session.WriteEntry: "entry", session.WriteTask: "task", session.WriteSubmission: "submission"}[c.Table])
			o.Set("value", c.Record)
		case session.ChangeDocument:
			o.Set("type", "document")
			o.Set("record", c.Record)
			if c.ConversationID != 0 {
				o.Set("conversationId", float64(c.ConversationID))
			}
			if c.Value == nil {
				o.Set("value", nil)
			} else {
				o.Set("version", float64(c.Version))
				o.Set("value", c.Value)
			}
			o.Set("ops", delta.OpsValue(c.Ops))
		case session.ChangeDocumentCopy:
			o.Set("type", "document.copy")
			o.Set("record", c.Record)
			o.Set("conversationId", float64(c.ConversationID))
			source := jv.NewObject()
			source.Set("id", float64(c.Source.ID))
			if c.Source.At.Current {
				source.Set("at", "current")
			} else {
				source.Set("at", float64(c.Source.At.Seq))
			}
			o.Set("source", source)
		}
		out = append(out, o)
	}
	return out
}
