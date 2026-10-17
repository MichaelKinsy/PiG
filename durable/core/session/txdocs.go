package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

type forkCopy struct {
	record *jv.Object
	source DocCopySource
}

// prepareForkDocumentCopies is session/forks.ts prepareForkDocumentCopies.
func (tx *Tx) prepareForkDocumentCopies(parent, at, child ID) ([]forkCopy, error) {
	entry, seq, err := tx.s.store.EntryIn(parent, at)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, errorf("Entry %d is not visible from conversation %d", at, parent)
	}
	var copies []forkCopy
	seen := map[string]bool{}
	collect := func(scope Scope, point Point, policy ForkPolicy) error {
		var after ID
		for {
			page, err := tx.s.store.ScanDocuments(scope, point, "", internalScanPage, after)
			if err != nil {
				return err
			}
			for _, src := range page.Items {
				srcFork, _ := getStr(src, "fork")
				if sk, _ := getStr(getObj(src, "scope"), "kind"); sk != "conversation" || ForkPolicy(srcFork) != policy {
					continue
				}
				id := tx.mint()
				kind, _ := getStr(src, "kind")
				rec := obj("id", idv(id), "kind", kind)
				if key, ok := getStr(src, "key"); ok {
					rec.Set("key", key)
				}
				rec.Set("scope", obj("kind", "conversation", "conversationId", idv(child)))
				history, _ := getStr(src, "history")
				rec.Set("history", history)
				rec.Set("fork", srcFork)
				addr := addressID(recordAddress(rec))
				if seen[addr] {
					member := kind
					if key, ok := getStr(rec, "key"); ok {
						member = kind + "/" + key
					}
					return errorf("Fork selects multiple source documents for %s", member)
				}
				seen[addr] = true
				copies = append(copies, forkCopy{rec, DocCopySource{ID: mustInt(src, "id"), At: point}})
			}
			if page.After == 0 {
				return nil
			}
			after = page.After
		}
	}
	entryConv := mustInt(entry, "conversationId")
	if err := collect(Scope{Kind: ScopeConversation, Owner: entryConv}, AsOf(seq), ForkAsOf); err != nil {
		return nil, err
	}
	if err := collect(Scope{Kind: ScopeConversation, Owner: parent}, Current, ForkCurrent); err != nil {
		return nil, err
	}
	return copies, nil
}

func (tx *Tx) assertTaskDocumentsOpen(a Address) error {
	if a.Scope.Kind != ScopeTask {
		return nil
	}
	if t := tx.tasks[a.Scope.Owner]; t != nil && t.write != nil && taskStatus(t.write) == StatusTerminal {
		return errorf("Task %d is terminal", a.Scope.Owner)
	}
	return nil
}

// Doc returns the draft of a document, creating the document when absent. A
// second call for the same address in one transaction returns the same draft.
func (tx *Tx) Doc(def *DocDef, args DocArgs) (*delta.Obj, error) {
	if err := tx.assertOpen(); err != nil {
		return nil, err
	}
	addr := def.address(args)
	if err := tx.assertTaskDocumentsOpen(addr); err != nil {
		return nil, err
	}
	id := addressID(addr)
	latest := tx.latestByAddr[id]
	if latest != nil && !latest.retireOnCommit {
		if latest.draft != nil {
			tx.charge(hopcost.TxDoc(tx.s.hop, true, 0, 0, false, false))
			return latest.draft, nil
		}
		if latest.target != nil && latest.target.kind == targetForkCopy {
			tx.charge(hopcost.TxDoc(tx.s.hop, false, hopcost.DocStored, 0, false, false))
		}
		if latest.target != nil && latest.target.kind == targetForkCopy {
			draft, err := tx.acquireForkCopy(latest, def)
			if err != nil {
				return nil, err
			}
			latest.draft = draft
			return draft, nil
		}
	}
	var seed any
	if def.Family {
		seed = jv.Clone(args.Seed)
	}
	e := &docEntry{addressID: id, address: addr, def: def}
	before := len(tx.documents)
	tx.documents = append(tx.documents, e)
	if tx.latestByAddr == nil {
		tx.latestByAddr = map[string]*docEntry{}
	}
	tx.latestByAddr[id] = e
	load := hopcost.DocAbsent
	if c := tx.s.docs[id]; c != nil && c.valueVersion == def.Version {
		load = hopcost.DocCached
	}
	var scope byte
	switch addr.Scope.Kind {
	case ScopeConversation:
		scope = 'c'
	case ScopeTask:
		scope = 't'
	}
	createdHere := tx.createdConvs[addr.Scope.Owner]
	taskStaged, _ := tx.taskTicksState(addr.Scope.Owner)
	draft, err := tx.acquire(e, seed, latest != nil && latest.retireOnCommit)
	if err == nil {
		if load == hopcost.DocAbsent && e.target != nil && e.target.kind == targetLoaded {
			load = hopcost.DocStored
		}
		tx.charge(hopcost.TxDoc(tx.s.hop, false, load, scope, createdHere, taskStaged))
	}
	if err != nil {
		if _, need := IsNeedRead(err); need {
			// The call runs again once the host answers: it leaves no document entry behind.
			tx.documents = tx.documents[:before]
			if latest == nil {
				delete(tx.latestByAddr, id)
			} else {
				tx.latestByAddr[id] = latest
			}
		}
		return nil, err
	}
	e.draft = draft
	return draft, nil
}

func (tx *Tx) acquire(e *docEntry, seed any, skipLoad bool) (*delta.Obj, error) {
	def := e.def
	var loaded *loadedDoc
	if !skipLoad {
		var err error
		if loaded, err = tx.s.loadDocument(def, e.addressID, e.address); err != nil {
			return nil, err
		}
	}
	if loaded != nil {
		if err := checkRecordScope(def, loaded.record); err != nil {
			return nil, err
		}
		if err := checkRecordVersion(def, loaded.record, loaded.storedVersion); err != nil {
			return nil, err
		}
		e.target = &docTarget{kind: targetLoaded, loaded: loaded}
		e.change = loaded.tracker.BeginChange()
		return e.change.State(), nil
	}
	switch e.address.Scope.Kind {
	case ScopeConversation:
		if err := tx.requireConversation(e.address.Scope.Owner); err != nil {
			return nil, err
		}
	case ScopeTask:
		task, err := tx.currentTask(e.address.Scope.Owner)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, errorf("Task %d does not exist", e.address.Scope.Owner)
		}
		if taskStatus(task) == StatusTerminal {
			return nil, errorf("Task %d is terminal", e.address.Scope.Owner)
		}
	}
	var value *jv.Object
	if def.HostInitial != nil {
		var err error
		if value, err = def.HostInitial(seed); err != nil {
			return nil, err
		}
	} else {
		value = def.Initial(seed)
	}
	if value == nil {
		return nil, errors.New("document initial returned no value")
	}
	value = jv.Clone(value).(*jv.Object)
	docID := tx.mint()
	tracker := delta.Track(value)
	e.target = &docTarget{kind: targetCreated, record: documentCreate(def, e.address, docID), version: def.Version, tracker: tracker}
	e.change = tracker.BeginChange()
	return e.change.State(), nil
}

func (tx *Tx) acquireForkCopy(e *docEntry, def *DocDef) (*delta.Obj, error) {
	t := e.target
	stored, err := tx.s.store.Document(t.source.ID, t.source.At)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, errorf("Fork source document %d cannot be read", t.source.ID)
	}
	if !sameCopiedRecord(stored.Record, t.record) {
		return nil, errorf("Fork source document %d does not match the copied record", t.source.ID)
	}
	value, err := materialize(def, t.record, stored.Version, stored.Value)
	if err != nil {
		return nil, err
	}
	tracker := delta.Track(value)
	e.def = def
	e.target = &docTarget{kind: targetCreated, record: t.record, version: def.Version, tracker: tracker}
	e.change = tracker.BeginChange()
	return e.change.State(), nil
}

func sameCopiedRecord(stored, copied *jv.Object) bool {
	if sk, _ := getStr(getObj(stored, "scope"), "kind"); sk != "conversation" {
		return false
	}
	for _, k := range []string{"kind", "key", "history", "fork"} {
		a, ha := stored.Get(k)
		b, hb := copied.Get(k)
		if ha != hb || !jv.Equal(a, b) {
			return false
		}
	}
	return true
}

// RetireDoc retires a document at commit; an absent document is a no-op.
func (tx *Tx) RetireDoc(def *DocDef, args DocArgs) error {
	if err := tx.assertOpen(); err != nil {
		return err
	}
	addr := def.address(args)
	id := addressID(addr)
	latest := tx.latestByAddr[id]
	if latest != nil && latest.retireOnCommit {
		return nil
	}
	if latest != nil && latest.target != nil && latest.target.kind == targetForkCopy {
		if err := checkRecordScope(def, latest.target.record); err != nil {
			return err
		}
		latest.retireOnCommit = true
		return nil
	}
	if latest != nil && latest.draft != nil {
		latest.retireOnCommit = true
		return nil
	}
	e := &docEntry{addressID: id, address: addr, def: def, retireOnCommit: true}
	tx.documents = append(tx.documents, e)
	if tx.latestByAddr == nil {
		tx.latestByAddr = map[string]*docEntry{}
	}
	tx.latestByAddr[id] = e
	var rec *jv.Object
	if cached := tx.s.docs[id]; cached != nil {
		tx.s.touchDocument(id)
		rec = cached.record
	} else {
		var err error
		if rec, err = tx.s.store.FindDocument(addr, Current); err != nil {
			return err
		}
	}
	if rec == nil {
		return nil
	}
	if err := checkRecordScope(def, rec); err != nil {
		return err
	}
	e.target = &docTarget{kind: targetRetireOnly, record: rec}
	return nil
}

//go:noinline
func planDocument(e *docEntry) *docPlan {
	t := e.target
	if t == nil {
		return nil
	}
	plan := &docPlan{addressID: e.addressID, retire: e.retireOnCommit}
	switch t.kind {
	case targetCreated:
		plan.record = t.record
		plan.content = &Write{Kind: WriteDocCreate, Record: t.record, Version: t.version, Base: true, Value: e.prepared.Value()}
		plan.change = &planChange{tracker: t.tracker, prepared: e.prepared, version: t.version}
	case targetForkCopy:
		plan.record = t.record
		plan.content = &Write{Kind: WriteDocCopy, Record: t.record, Source: t.source}
	case targetRetireOnly:
		plan.record, plan.committed = t.record, true
	case targetLoaded:
		l := t.loaded
		plan.record, plan.committed = l.record, true
		version := e.def.Version
		id := mustInt(l.record, "id")
		var content *Write
		switch {
		case l.storedVersion < version:
			content = &Write{Kind: WriteDocChange, ID: id, Version: version, Base: true, Value: e.prepared.Value()}
		case len(e.prepared.Ops()) > 0:
			content = &Write{Kind: WriteDocChange, ID: id, Version: version, Ops: e.prepared.Ops()}
		}
		plan.content = content
		plan.change = &planChange{tracker: l.tracker, prepared: e.prepared, version: version, loaded: l, def: e.def}
	}
	return plan
}

func (p *docPlan) publishes() bool {
	return p.retire || p.change == nil || p.change.loaded == nil || p.content != nil
}

func (tx *Tx) abortChanges() {
	for _, d := range tx.documents {
		if d.change != nil {
			d.change.Abort()
		}
	}
}

func (tx *Tx) settleFailure() {
	tx.sealed = true
	tx.abortChanges()
}

// settleSuccess prepares every document change and assembles the writes.
//
//go:noinline
func (tx *Tx) settleSuccess() ([]Write, error) {
	tx.sealed = true
	writes, err := tx.settle()
	if err != nil {
		tx.abortChanges()
		return nil, err
	}
	return writes, nil
}

//go:noinline
func (tx *Tx) settle() ([]Write, error) {
	for _, d := range tx.documents {
		if d.change != nil {
			prepare := d.change.Prepare
			if d.hasExternal {
				prepare = func() (*delta.Prepared, error) { return d.change.PrepareThen(d.external) }
			}
			p, err := prepare()
			if err != nil {
				return nil, err
			}
			d.prepared = p
		}
	}
	return tx.assemble()
}

//go:noinline
func (tx *Tx) assemble() ([]Write, error) {
	for _, d := range tx.documents {
		if p := planDocument(d); p != nil {
			tx.plans = append(tx.plans, p)
		}
	}
	if err := tx.rejectForkSourceWrites(); err != nil {
		return nil, err
	}
	if err := tx.validateOwners(); err != nil {
		return nil, err
	}
	for _, id := range tx.taskOrder {
		t := tx.tasks[id]
		if t.write == nil || t.writeIsNew {
			continue
		}
		if t.hasCommitted {
			tx.settleCached++
		}
		committed, err := tx.committedTask(id)
		if err != nil {
			return nil, err
		}
		if committed == nil {
			return nil, errorf("Task %d does not exist", id)
		}
		if taskStatus(committed) == StatusTerminal {
			return nil, errorf("Task %d is already terminal", id)
		}
		if mustInt(committed, "conversationId") != mustInt(t.write, "conversationId") {
			return nil, errorf("Task %d cannot change conversations", id)
		}
	}

	terminal := map[ID]bool{}
	for _, id := range tx.taskOrder {
		if w := tx.tasks[id].write; w != nil && taskStatus(w) == StatusTerminal {
			terminal[id] = true
		}
	}
	if len(terminal) > 0 {
		retiring := map[ID]bool{}
		for _, p := range tx.plans {
			scope := getObj(p.record, "scope")
			if sk, _ := getStr(scope, "kind"); sk != "task" || !terminal[mustInt(scope, "taskId")] {
				continue
			}
			p.retire = true
			retiring[mustInt(p.record, "id")] = true
		}
		for _, taskID := range tx.taskOrder {
			if !terminal[taskID] {
				continue
			}
			if tx.tasks[taskID].writeIsNew {
				continue
			}
			var after ID
			for {
				page, err := tx.s.store.ScanDocuments(Scope{Kind: ScopeTask, Owner: taskID}, Current, "", internalScanPage, after)
				if err != nil {
					return nil, err
				}
				for _, rec := range page.Items {
					rid := mustInt(rec, "id")
					if retiring[rid] {
						continue
					}
					tx.plans = append(tx.plans, &docPlan{addressID: addressID(recordAddress(rec)), record: rec, committed: true, retire: true})
					retiring[rid] = true
				}
				if page.After == 0 {
					break
				}
				after = page.After
			}
		}
	}

	for _, p := range tx.plans {
		if !p.publishes() {
			continue
		}
		scope := getObj(p.record, "scope")
		sk, _ := getStr(scope, "kind")
		if sk == "conversation" {
			p.convID = mustInt(scope, "conversationId")
		}
		if sk != "task" {
			continue
		}
		tid := mustInt(scope, "taskId")
		t := tx.taskEntry(tid)
		if t.pubConv == 0 {
			cur, err := tx.currentTask(tid)
			if err != nil {
				return nil, err
			}
			if cur != nil {
				t.pubConv = mustInt(cur, "conversationId")
			}
		}
		p.convID = t.pubConv
	}

	for _, ch := range tx.submissionChanges {
		cur := tx.submissions[ch.id]
		if cur == nil {
			var err error
			tx.settleReads++
			if cur, err = tx.s.store.Submission(ch.id); err != nil {
				return nil, err
			}
		}
		if cur == nil {
			return nil, errorf("Submission %d does not exist", ch.id)
		}
		next, err := applySubmissionChange(cur, ch)
		if err != nil {
			return nil, err
		}
		if next != cur {
			tx.setSubmission(ch.id, next)
		}
	}

	// An upper bound of the writes still to come (a plan adds at most two), allocated at once on the Go builds (growWrites).
	writes := growWrites(tx.writes, len(tx.submissionOrder)+len(tx.taskOrder)+2*len(tx.plans))
	for _, id := range tx.submissionOrder {
		writes = append(writes, Write{Kind: WriteSubmission, Record: tx.submissions[id]})
	}
	for _, id := range tx.taskOrder {
		if w := tx.tasks[id].write; w != nil {
			writes = append(writes, Write{Kind: WriteTask, Record: w})
		}
	}
	for _, p := range tx.plans {
		if c := p.change; c != nil && p.content != nil && p.content.Kind == WriteDocChange && !p.content.Base && c.loaded != nil {
			if c.def != nil && c.def.CheckpointWhen != nil && c.def.CheckpointWhen(c.prepared.Value(), c.prepared.Ops(), c.loaded.deltasSinceBase) {
				p.content = &Write{Kind: WriteDocChange, ID: p.content.ID, Version: c.version, Base: true, Value: c.prepared.Value()}
			}
		}
		if p.content != nil {
			writes = append(writes, *p.content)
		}
		if p.retire {
			writes = append(writes, Write{Kind: WriteDocRetire, ID: mustInt(p.record, "id")})
		}
	}
	return writes, nil
}

func applySubmissionChange(cur *jv.Object, ch submissionChange) (*jv.Object, error) {
	status, _ := getStr(cur, "status")
	if status == "done" || status == "unanswered" {
		return cur, nil
	}
	id := mustInt(cur, "id")
	if ch.place {
		if status != "queued" {
			return nil, errorf("Submission %d is not queued", id)
		}
		next := "done"
		if typ, _ := getStr(cur, "type"); typ == "input" {
			next = "placed"
		}
		return with(cur, "status", next, "entry", idv(ch.entry)), nil
	}
	if st, _ := getStr(ch.settle, "status"); st == "done" && status != "placed" {
		return nil, errorf("Submission %d is not a placed input", id)
	}
	out := spread(cur)
	ch.settle.Entries(func(k string, v any) bool { out.Set(k, v); return true })
	return out, nil
}

//go:noinline
func (tx *Tx) validateOwners() error {
	type owner struct {
		what string
		id   ID
	}
	var owners []owner
	for _, w := range tx.writes {
		if w.Kind != WriteConversation {
			continue
		}
		if o := getObj(w.Record, "owner"); o != nil {
			owners = append(owners, owner{"Conversation owner task", mustInt(o, "taskId")})
		}
	}
	for _, id := range tx.taskOrder {
		t := tx.tasks[id]
		if t.write != nil && t.writeIsNew {
			if o, ok := getInt(t.write, "owner"); ok {
				owners = append(owners, owner{"Task owner", o})
			}
		}
	}
	for _, o := range owners {
		task, err := tx.currentTask(o.id)
		if err != nil {
			return err
		}
		if task == nil {
			return errorf("%s %d does not exist", o.what, o.id)
		}
		if st := taskStatus(task); st == StatusTerminal || st == StatusCompleting {
			return errorf("%s %d is %s", o.what, o.id, string(st))
		}
		if getBool(task, "abortRequested") {
			return errorf("%s %d is abort-marked", o.what, o.id)
		}
	}
	return nil
}

//go:noinline
func (tx *Tx) rejectForkSourceWrites() error {
	for _, p := range tx.plans {
		if p.content == nil && !p.retire {
			continue
		}
		rid := mustInt(p.record, "id")
		if tx.forkSourceDocs[rid] {
			return errorf("Cannot change fork source document %d in the fork transaction", rid)
		}
		scope := getObj(p.record, "scope")
		if sk, _ := getStr(scope, "kind"); sk == "conversation" && tx.forkSourceConvs[mustInt(scope, "conversationId")] {
			if f, _ := getStr(p.record, "fork"); f == "current" {
				return errorf("Cannot fork conversation %d while changing its current-policy documents", mustInt(scope, "conversationId"))
			}
		}
	}
	return nil
}

// ChangeKind is the kind of a published change.
type ChangeKind uint8

// The published change kinds.
const (
	ChangeTable ChangeKind = iota + 1
	ChangeDocument
	ChangeDocumentCopy
)

// Change is one published change of a commit: a table write, a document
// change or a document copy.
type Change struct {
	Kind   ChangeKind
	Table  WriteKind // ChangeTable: the write's kind (its record is Record)
	Record *jv.Object
	// ConversationID is the conversation a document change belongs to (0: none).
	ConversationID ID
	Version        int
	Value          *jv.Object // nil: the document was retired
	Ops            []delta.Op
	Source         DocCopySource
}

// adopt applies the commit's effect to the Session's caches and returns the
// document publications.
//
//go:noinline
func (tx *Tx) adopt(seq Seq) ([]Change, error) {
	var pubs []Change
	for _, p := range tx.plans {
		record := p.record
		if !p.committed {
			record = with(record, "createdAt", idv(seq))
		}
		if p.retire {
			record = with(record, "retiredAt", idv(seq))
		}
		if c := p.change; c != nil {
			needAdopt := false
			if c.loaded == nil {
				needAdopt = !p.retire
			} else {
				needAdopt = len(c.prepared.Ops()) > 0
			}
			if needAdopt {
				if err := c.tracker.Adopt(c.prepared); err != nil {
					return nil, err
				}
			} else {
				c.prepared.Abort()
			}
			if c.loaded != nil {
				if c.loaded.storedVersion < c.version {
					c.loaded.storedVersion = c.version
				}
				if p.content != nil && p.content.Kind == WriteDocChange {
					if p.content.Base {
						c.loaded.deltasSinceBase = 0
					} else {
						c.loaded.deltasSinceBase++
					}
				}
			} else if !p.retire {
				tx.s.docs[p.addressID] = &loadedDoc{addressID: p.addressID, record: record, storedVersion: c.version, valueVersion: c.version, tracker: c.tracker}
				tx.s.touchDocument(p.addressID)
			}
		}
		switch {
		case p.retire:
			if p.committed {
				if cur := tx.s.docs[p.addressID]; cur != nil && mustInt(cur.record, "id") == mustInt(record, "id") {
					tx.s.dropDocument(p.addressID)
				}
			}
			pubs = append(pubs, Change{Kind: ChangeDocument, Record: record, ConversationID: p.convID})
		case p.content != nil && p.content.Kind == WriteDocCopy:
			pubs = append(pubs, Change{Kind: ChangeDocumentCopy, Record: record, ConversationID: p.convID, Source: p.content.Source})
		case p.change != nil && p.publishes():
			var ops []delta.Op
			if p.change.loaded != nil {
				ops = p.change.prepared.Ops()
			}
			pubs = append(pubs, Change{Kind: ChangeDocument, Record: record, ConversationID: p.convID, Version: p.change.version, Value: p.change.prepared.Value(), Ops: ops})
		}
	}
	return pubs, nil
}
