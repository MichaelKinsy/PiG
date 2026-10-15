package storage_test

// pi: packages/durable/src/session/transaction.ts (createConversation, forkConversation, appendEntry and the table reads) and session.ts (commit)

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pigTx runs "tx" steps on Pig's Session, naming ids as the Pi driver does ({lit} or {ref}, a created conversation or entry modulo the count).
type pigTx struct {
	session *session.SessionImpl
	convs   []int64
	entries []int64
	owner   map[int64]int64 // entry id to its conversation
	tasks   []int64
	subs    []int64
}

var probeTask = durable.DefineTask(durable.TaskDefinition[durable.JsonValue, durable.JsonValue, durable.JsonValue, struct{}]{
	Name:    "probe",
	Version: 1,
	Initial: func(input durable.JsonValue) durable.JsonValue {
		return map[string]any{"phase": "start", "input": input}
	},
})

func newPigTx(store durable.Storage) *pigTx {
	return &pigTx{session: session.CreateSession(store), owner: map[int64]int64{}}
}

type txOp struct {
	T          string                       `json:"t"`
	Ownership  *txOwnership                 `json:"ownership"`
	Parent     *txRef                       `json:"parent"`
	At         *txRef                       `json:"at"`
	Conv       *txRef                       `json:"conv"`
	ID         *txRef                       `json:"id"`
	Draft      *txDraft                     `json:"draft"`
	Query      json.RawMessage              `json:"query"`
	Limit      int                          `json:"limit"`
	Input      json.RawMessage              `json:"input"`
	Background bool                         `json:"background"`
	Create     json.RawMessage              `json:"create"`
	Entry      *txRef                       `json:"entry"`
	Settlement json.RawMessage              `json:"settlement"`
	Tok        string                       `json:"tok"`
	Owner      *txRef                       `json:"owner"`
	Key        string                       `json:"key"`
	Seed       json.RawMessage              `json:"seed"`
	NoSeed     bool                         `json:"noseed"`
	Mut        []map[string]json.RawMessage `json:"mut"`
}

func probeDocInitial() durable.JsonObject {
	return delta.JsonObjectOf("count", 0, "items", []any{})
}

func probeDoc(kind string, semantics durable.DocumentSemantics) durable.DocToken[durable.JsonObject] {
	return durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
		CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{Kind: kind, Version: 1, Initial: probeDocInitial},
		DocumentSemantics:   semantics,
	})
}

// probeDocs mirror docTokens in testdata/memory_oracle.mjs.
var probeDocs = map[string]durable.AnyDocToken{
	"s": probeDoc("p.s", durable.DocumentSemantics{Scope: durable.ScopeSession}),
	"c": probeDoc("p.c", durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent}),
	"i": probeDoc("p.i", durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial}),
	"r": probeDoc("p.r", durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf}),
	"k": probeDoc("p.k", durable.DocumentSemantics{Scope: durable.ScopeTask}),
	"g": durable.DefineDocFamily(durable.DocFamilyDefinition[durable.JsonObject, durable.JsonValue]{
		Family: true, Kind: "p.g", Version: 1, DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
		Initial: func(seed durable.JsonValue) durable.JsonObject {
			return delta.JsonObjectOf("seed", seed, "count", 0, "items", []any{})
		},
	}),
	"f": durable.DefineDocFamily(durable.DocFamilyDefinition[durable.JsonObject, durable.JsonValue]{
		Family: true, Kind: "p.f", Version: 1, DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
		Initial: func(seed durable.JsonValue) durable.JsonObject {
			return delta.JsonObjectOf("seed", seed, "count", 0, "items", []any{})
		},
	}),
}

type txOwnership struct {
	Kind    string `json:"kind"`
	TaskID  int64  `json:"taskId"`
	TaskRef *int   `json:"taskRef"`
}

type txRef struct {
	Lit *int64 `json:"lit,omitempty"`
	Ref int    `json:"ref"`
	Of  bool   `json:"of,omitempty"`
	Anc bool   `json:"anc,omitempty"`
}

type txDraft struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
	Head json.RawMessage `json:"head"`
}

func conversationIdOf(record durable.EntryRecord) durable.ConversationId {
	return record.ConversationId
}

func pickID(ref *txRef, list []int64) int64 {
	if ref.Lit != nil {
		return *ref.Lit
	}
	if len(list) == 0 {
		return 1_000_000_000
	}
	return list[ref.Ref%len(list)]
}

func (p *pigTx) ownership(o *txOwnership, tasks []int64) durable.ConversationOwnership {
	if o.Kind == "task" {
		id := o.TaskID
		if o.TaskRef != nil {
			id = pickID(&txRef{Ref: *o.TaskRef}, tasks)
		}
		return durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: durable.TaskId(id)}
	}
	return durable.ConversationOwnership{Kind: durable.ConversationOwnerless}
}

func (p *pigTx) run(t *testing.T, step oracleStep) oracleResult {
	t.Helper()
	var scope session.TransactionScope
	if step.Scope != nil {
		if step.Scope.Conv != nil {
			scope.ConversationId = new(durable.ConversationId(pickID(step.Scope.Conv, p.convs)))
		}
		if step.Scope.Task != nil {
			scope.TaskId = new(durable.TaskId(pickID(step.Scope.Task, p.tasks)))
		}
	}
	var pendingConvs, pendingEntries []int64
	pendingOwner := map[int64]int64{}
	var pendingTasks, pendingSubs []int64
	value, err := p.session.CommitWith(context.Background(), func(tx *session.Transaction) (any, error) {
		outcomes := make([]map[string]any, 0, len(step.Ops))
		for _, raw := range step.Ops {
			var op txOp
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatal(err)
			}
			convs := append(append([]int64(nil), p.convs...), pendingConvs...)
			entries := append(append([]int64(nil), p.entries...), pendingEntries...)
			tasks := append(append([]int64(nil), p.tasks...), pendingTasks...)
			subs := append(append([]int64(nil), p.subs...), pendingSubs...)
			var result any
			var present = true
			var opErr error
			switch op.T {
			case "root":
				result, opErr = tx.CreateRootConversation()
			case "create":
				var record durable.ConversationRecord
				if record, opErr = tx.CreateConversation(durable.CreateConversationOptions{Ownership: p.ownership(op.Ownership, tasks)}); opErr == nil {
					pendingConvs = append(pendingConvs, int64(record.Id))
				}
				result = record
			case "fork":
				var record durable.ConversationRecord
				parent := pickID(op.Parent, convs)
				at := entries
				if op.At.Of || op.At.Anc {
					at = nil
					for _, id := range entries {
						conversation, known := pendingOwner[id]
						if !known {
							conversation = p.owner[id]
						}
						if (conversation == parent) == op.At.Of {
							at = append(at, id)
						}
					}
				}
				record, opErr = tx.ForkConversation(durable.ConversationId(parent), durable.EntryId(pickID(op.At, at)), durable.CreateConversationOptions{Ownership: p.ownership(op.Ownership, tasks)})
				if opErr == nil {
					pendingConvs = append(pendingConvs, int64(record.Id))
				}
				result = record
			case "append":
				draft := durable.EntryDraft{Kind: op.Draft.Kind}
				if len(op.Draft.Data) > 0 {
					if err := json.Unmarshal(op.Draft.Data, &draft.Data); err != nil {
						t.Fatal(err)
					}
				}
				if len(op.Draft.Head) > 0 {
					if string(op.Draft.Head) == `"self"` {
						draft.HeadSelf = true
					} else {
						var ref txRef
						if err := json.Unmarshal(op.Draft.Head, &ref); err != nil {
							t.Fatal(err)
						}
						draft.Head = new(durable.EntryId(pickID(&ref, entries)))
					}
				}
				var record durable.EntryRecord
				if record, opErr = tx.AppendEntry(durable.ConversationId(pickID(op.Conv, convs)), draft); opErr == nil {
					pendingEntries = append(pendingEntries, int64(record.Id))
					pendingOwner[int64(record.Id)] = int64(conversationIdOf(record))
				}
				result = record
			case "createTask":
				ownership := durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}
				if op.Ownership.Kind == "task" {
					ownership = durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: durable.TaskId(pickID(&txRef{Ref: *op.Ownership.TaskRef}, tasks))}
				}
				options := durable.TaskOptions{Ownership: ownership, Background: op.Background}
				if op.Conv != nil {
					options.ConversationId = new(durable.ConversationId(pickID(op.Conv, convs)))
				}
				var input durable.JsonValue
				if err := json.Unmarshal(op.Input, &input); err != nil {
					t.Fatal(err)
				}
				var id durable.TaskId
				if id, opErr = tx.CreateTaskErased(probeTask.Erased(), input, options); opErr == nil {
					pendingTasks = append(pendingTasks, int64(id))
				}
				result = id
			case "createSubmission":
				var create durable.SubmissionCreate
				if err := json.Unmarshal(op.Create, &create); err != nil {
					t.Fatal(err)
				}
				create.ConversationId = durable.ConversationId(pickID(op.Conv, convs))
				if op.Entry != nil {
					create.Entry = new(durable.EntryId(pickID(op.Entry, entries)))
				}
				var record durable.SubmissionRecord
				if record, opErr = tx.CreateSubmission(create); opErr == nil {
					pendingSubs = append(pendingSubs, int64(record.Id))
				}
				result = record
			case "settle":
				var settlement durable.SubmissionSettlement
				if err := json.Unmarshal(op.Settlement, &settlement); err != nil {
					t.Fatal(err)
				}
				if settlement.Answer != 0 {
					settlement.Answer = durable.EntryId(pickID(&txRef{Ref: int(settlement.Answer) - 1}, entries))
				}
				opErr = tx.SettleSubmission(durable.SubmissionId(pickID(op.ID, subs)), settlement)
				present = false
			case "place":
				opErr = tx.PlaceSubmission(durable.SubmissionId(pickID(op.ID, subs)), durable.EntryId(pickID(op.Entry, entries)))
				present = false
			case "doc", "retire":
				var args []any
				switch op.Tok {
				case "c", "i", "r", "g":
					args = append(args, durable.ConversationId(pickID(op.Owner, convs)))
					if op.Tok == "g" {
						var seed durable.JsonValue
						if len(op.Seed) > 0 {
							if err := json.Unmarshal(op.Seed, &seed); err != nil {
								t.Fatal(err)
							}
						}
						args = append(args, op.Key)
						if !op.NoSeed {
							args = append(args, seed)
						}
					}
				case "k":
					args = append(args, durable.TaskId(pickID(op.Owner, tasks)))
				case "f":
					var seed durable.JsonValue
					if len(op.Seed) > 0 {
						if err := json.Unmarshal(op.Seed, &seed); err != nil {
							t.Fatal(err)
						}
					}
					args = append(args, op.Key)
					if !op.NoSeed {
						args = append(args, seed)
					}
				}
				if op.T == "retire" {
					opErr = tx.RetireDoc(probeDocs[op.Tok], args...)
					present = false
					break
				}
				var draft *delta.Object
				if draft, opErr = tx.Doc(probeDocs[op.Tok], args...); opErr == nil {
				mutations:
					for _, mutation := range op.Mut {
						switch {
						case mutation["set"] != nil:
							var value any
							if err := json.Unmarshal(mutation["set"], &value); err != nil {
								t.Fatal(err)
							}
							if opErr = draft.Set("count", value); opErr != nil {
								break mutations
							}
						case mutation["push"] != nil:
							var value any
							if err := json.Unmarshal(mutation["push"], &value); err != nil {
								t.Fatal(err)
							}
							if _, opErr = draft.Array("items").Push(value); opErr != nil {
								break mutations
							}
						case mutation["pop"] != nil:
							draft.Array("items").Pop()
						case mutation["del"] != nil:
							draft.Delete("count")
						}
					}
					result = draft.Snapshot()
				}
			case "task":
				var record *session.AnyTaskRecord
				record, opErr = tx.Task(durable.TaskId(pickID(op.ID, tasks)))
				result, present = record, record != nil
			case "submission":
				var record *durable.SubmissionRecord
				record, opErr = tx.Submission(durable.SubmissionId(pickID(op.ID, subs)))
				result, present = record, record != nil
			case "conversation":
				var record *durable.ConversationRecord
				record, opErr = tx.Conversation(durable.ConversationId(pickID(op.ID, convs)))
				result, present = record, record != nil
			case "entry":
				var record *durable.EntryRecord
				record, opErr = tx.Entry(durable.EntryId(pickID(op.ID, entries)))
				result, present = record, record != nil
			case "scanEntries":
				var query durable.EntryQuery
				if err := json.Unmarshal(op.Query, &query); err != nil {
					t.Fatal(err)
				}
				var page durable.Page[durable.EntryRecord, durable.Cursor]
				page, opErr = tx.ScanEntries(query, op.Limit, nil)
				result = pagedOracle(page)
			case "scanConversations":
				var query durable.ConversationQuery
				if err := json.Unmarshal(op.Query, &query); err != nil {
					t.Fatal(err)
				}
				var page durable.Page[durable.ConversationRecord, durable.Cursor]
				page, opErr = tx.ScanConversations(query, op.Limit, nil)
				result = pagedOracle(page)
			case "latestHead":
				var record *durable.EntryRecord
				record, opErr = tx.LatestHeadMarker(durable.ConversationId(pickID(op.Conv, convs)))
				result, present = record, record != nil
			default:
				t.Fatalf("unknown tx op %s", op.T)
			}
			switch {
			case opErr != nil:
				outcomes = append(outcomes, map[string]any{"err": opErr.Error()})
			case !present:
				outcomes = append(outcomes, map[string]any{"undef": true})
			default:
				outcomes = append(outcomes, map[string]any{"ok": result})
			}
		}
		if step.Fail {
			return nil, errors.New("rollback")
		}
		return outcomes, nil
	}, scope)
	// A failed Session closes its Storage on the microtasks that follow the failure; Pig's close runs on its own goroutine, so
	// the next step waits for it as the oracle's awaited step does.
	if p.session.Failure() != nil {
		<-p.session.Closed()
	}
	if err == nil {
		p.convs, p.entries = append(p.convs, pendingConvs...), append(p.entries, pendingEntries...)
		p.tasks, p.subs = append(p.tasks, pendingTasks...), append(p.subs, pendingSubs...)
		maps.Copy(p.owner, pendingOwner)
	}
	return settleOracle(value, true, err)
}

// sessionProgramFor builds a program of Session commits (with the odd failing one) interleaved with storage reads, so the ids the Session mints, the records it stages and what lands in storage are all compared with Pi's.
func sessionProgramFor(seed int64) oracleProgram {
	g := &oracleGen{random: rand.New(rand.NewSource(seed)), seqs: 15}
	ref := func() string { return fmt.Sprintf(`{"ref":%d}`, g.random.Intn(12)) }
	lit := func(max int) string { return fmt.Sprintf(`{"lit":%d}`, 1+g.random.Intn(max)) }
	pickRef := func(max int) string {
		if g.random.Intn(8) == 0 {
			return lit(max)
		}
		return ref()
	}
	ownership := func() string {
		switch g.random.Intn(10) {
		case 0:
			return fmt.Sprintf(`{"kind":"task","taskId":%d}`, 1+g.random.Intn(30))
		case 1, 2:
			return fmt.Sprintf(`{"kind":"task","taskRef":%d}`, g.random.Intn(6))
		}
		return `{"kind":"ownerless"}`
	}
	taskOwnership := func() string {
		if g.random.Intn(4) == 0 {
			return fmt.Sprintf(`{"kind":"task","taskRef":%d}`, g.random.Intn(6))
		}
		return `{"kind":"conversation"}`
	}
	txOp := func() json.RawMessage {
		switch g.random.Intn(30) {
		case 24, 25, 26, 27:
			tok := g.pick("s", "c", "i", "r", "k", "f", "g", "g")
			var muts []string
			for range g.random.Intn(4) {
				muts = append(muts, g.pick(`{"set":1}`, `{"set":2.5}`, `{"set":"x"}`, `{"push":1}`, `{"push":{"a":[1]}}`, `{"push":null}`, `{"pop":1}`, `{"del":1}`))
			}
			return json.RawMessage(fmt.Sprintf(`{"t":"doc","tok":%q,"owner":%s,"key":%q,"seed":%s,"noseed":%t,"mut":[%s]}`, tok, pickRef(8), g.pick("a", "b"), g.pick(`1`, `"s"`, `null`), g.random.Intn(6) == 0, strings.Join(muts, ",")))
		case 28, 29:
			return json.RawMessage(fmt.Sprintf(`{"t":"retire","tok":%q,"owner":%s,"key":%q}`, g.pick("s", "c", "i", "r", "k", "f", "g"), pickRef(8), g.pick("a", "b")))
		case 14, 15:
			conv := ""
			if g.random.Intn(2) == 0 {
				conv = `,"conv":` + pickRef(8)
			}
			return json.RawMessage(fmt.Sprintf(`{"t":"createTask","ownership":%s%s,"background":%t,"input":%s}`, taskOwnership(), conv, g.random.Intn(6) == 0, g.pick(`1`, `"x"`, `{"a":[1]}`)))
		case 16, 17:
			create := fmt.Sprintf(`{"type":"input","status":"queued"%s}`, g.pick(``, ``, `,"requestId":"r1"`, `,"requestId":"r2"`))
			extra := ""
			switch g.random.Intn(6) {
			case 0:
				create = `{"type":"input","status":"placed"}`
				extra = `,"entry":` + pickRef(30)
			case 1:
				create = `{"type":"input","status":"unanswered","reason":"gone"}`
			case 2:
				create = `{"type":"write","status":"queued"}`
			}
			return json.RawMessage(fmt.Sprintf(`{"t":"createSubmission","conv":%s,"create":%s%s}`, pickRef(8), create, extra))
		case 18:
			if g.random.Intn(2) == 0 {
				return json.RawMessage(fmt.Sprintf(`{"t":"settle","id":%s,"settlement":{"status":"done","answer":%d}}`, ref(), 1+g.random.Intn(12)))
			}
			return json.RawMessage(fmt.Sprintf(`{"t":"settle","id":%s,"settlement":{"status":"unanswered","reason":"r"%s}}`, ref(), g.pick(``, `,"detail":{"k":[1,null]}`, `,"detail":"d"`)))
		case 19:
			return json.RawMessage(fmt.Sprintf(`{"t":"place","id":%s,"entry":%s}`, ref(), pickRef(30)))
		case 20, 21:
			return json.RawMessage(fmt.Sprintf(`{"t":"task","id":%s}`, pickRef(8)))
		case 22, 23:
			return json.RawMessage(fmt.Sprintf(`{"t":"submission","id":%s}`, pickRef(8)))
		case 0, 1, 2:
			return json.RawMessage(fmt.Sprintf(`{"t":"create","ownership":%s}`, ownership()))
		case 3, 4:
			return json.RawMessage(fmt.Sprintf(`{"t":"fork","parent":%s,"at":%s,"ownership":%s}`, pickRef(8), g.pick(pickRef(30), `{"of":true,"ref":`+fmt.Sprint(g.random.Intn(12))+`}`, `{"of":true,"ref":`+fmt.Sprint(g.random.Intn(12))+`}`, `{"anc":true,"ref":`+fmt.Sprint(g.random.Intn(12))+`}`, `{"anc":true,"ref":`+fmt.Sprint(g.random.Intn(12))+`}`), ownership()))
		case 5, 6, 7, 8, 9:
			draft := fmt.Sprintf(`{"kind":%q`, g.pick("message", "note", "x"))
			if data := g.data(); data != "" {
				draft += `,"data":` + data
			}
			switch g.random.Intn(5) {
			case 0:
				draft += `,"head":"self"`
			case 1:
				draft += `,"head":` + pickRef(30)
			}
			return json.RawMessage(fmt.Sprintf(`{"t":"append","conv":%s,"draft":%s}`, pickRef(8), draft+`}`))
		case 10:
			return json.RawMessage(fmt.Sprintf(`{"t":%q,"id":%s}`, g.pick("conversation", "entry"), pickRef(30)))
		case 11:
			return json.RawMessage(fmt.Sprintf(`{"t":"latestHead","conv":%s}`, pickRef(8)))
		case 12:
			return json.RawMessage(fmt.Sprintf(`{"t":"scanEntries","query":{"conversationId":%d%s},"limit":%d}`, 1+g.random.Intn(8), g.pick(``, `,"order":"ascending"`, `,"maxEntryId":9`), []int{1, 3, 100}[g.random.Intn(3)]))
		default:
			return json.RawMessage(fmt.Sprintf(`{"t":"scanConversations","query":{%s},"limit":%d}`, g.pick(``, `"order":"ascending"`), []int{1, 3, 100}[g.random.Intn(3)]))
		}
	}
	var steps []oracleStep
	steps = append(steps, oracleStep{Op: "tx", Ops: []json.RawMessage{json.RawMessage(`{"t":"root"}`)}})
	for range 22 {
		if g.random.Intn(4) == 0 {
			switch g.random.Intn(8) {
			case 6, 7:
				scope := g.pick(`{"kind":"session"}`, `{"kind":"conversation","ref":`+fmt.Sprint(g.random.Intn(12))+`}`, `{"kind":"task","ref":`+fmt.Sprint(g.random.Intn(6))+`}`)
				steps = append(steps, oracleStep{Op: "scanDocuments", Query: json.RawMessage(`{"scope":` + scope + `,"at":` + g.point() + `}`), Limit: 100})
			case 4:
				steps = append(steps, oracleStep{Op: "task", ID: int64(1 + g.random.Intn(30))})
			case 5:
				steps = append(steps, oracleStep{Op: "submission", ID: int64(1 + g.random.Intn(30))})
			case 0:
				steps = append(steps, oracleStep{Op: "mintId"})
			case 1:
				steps = append(steps, oracleStep{Op: "conversation", ID: int64(1 + g.random.Intn(10))})
			case 2:
				steps = append(steps, oracleStep{Op: "entry", ID: int64(1 + g.random.Intn(30))})
			default:
				steps = append(steps, oracleStep{Op: "scanEntries", Query: json.RawMessage(fmt.Sprintf(`{"conversationId":%d}`, 1+g.random.Intn(8))), Limit: 100})
			}
			continue
		}
		step := oracleStep{Op: "tx", Fail: g.random.Intn(10) == 0}
		switch g.random.Intn(5) {
		case 0:
			step.Scope = &oracleScope{Conv: &txRef{Ref: g.random.Intn(12)}}
		case 1:
			step.Scope = &oracleScope{Task: &txRef{Ref: g.random.Intn(6)}}
		case 2:
			step.Scope = &oracleScope{Conv: &txRef{Ref: g.random.Intn(12)}, Task: &txRef{Ref: g.random.Intn(6)}}
		}
		for range 1 + g.random.Intn(5) {
			step.Ops = append(step.Ops, txOp())
		}
		steps = append(steps, step)
		if g.random.Intn(2) == 0 {
			scope := g.pick(`{"kind":"session"}`, `{"kind":"conversation","ref":`+fmt.Sprint(g.random.Intn(12))+`}`, `{"kind":"conversation","ref":`+fmt.Sprint(g.random.Intn(12))+`}`, `{"kind":"task","ref":`+fmt.Sprint(g.random.Intn(6))+`}`)
			steps = append(steps, oracleStep{Op: "scanDocuments", Query: json.RawMessage(`{"scope":` + scope + `,"at":` + g.point() + `}`), Limit: 100})
		}
	}
	return oracleProgram{Steps: steps}
}

var digits = regexp.MustCompile(`\d+`)

// sessionPoisonPrograms commit the root twice: the second commit fails after storage admission, which fails the Session, so every later commit must fail with SessionFailed.
func sessionPoisonPrograms() []oracleProgram {
	root := json.RawMessage(`{"t":"root"}`)
	create := json.RawMessage(`{"t":"create","ownership":{"kind":"ownerless"}}`)
	return []oracleProgram{{Steps: []oracleStep{
		{Op: "tx", Ops: []json.RawMessage{root}},
		{Op: "tx", Ops: []json.RawMessage{root}},
		{Op: "tx", Ops: []json.RawMessage{create}},
		{Op: "mintId"},
		{Op: "conversation", ID: 1},
		{Op: "tx", Ops: []json.RawMessage{create}},
	}}}
}

func TestSessionTransactionsMatchPiOnSeededPrograms(t *testing.T) {
	const seeded = 400
	programs := make([]oracleProgram, seeded)
	for i := range programs {
		programs[i] = sessionProgramFor(int64(20265050 + i))
	}
	programs = append(programs, sessionPoisonPrograms()...)
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	expected := runPiNode(t, "memory", payload)
	inner, failedSteps := map[string]int{}, 0
	for _, results := range expected {
		for _, result := range results {
			if result.Err != nil && strings.Contains(result.Err.Message, "Session failed after a storage error") {
				failedSteps++
			}
			for _, marker := range []string{`"kind":"p.s"`, `"kind":"p.c"`, `"kind":"p.i"`, `"kind":"p.r"`, `"kind":"p.k"`, `"kind":"p.f"`, `"kind":"p.g"`, `"retiredAt"`} {
				if bytes.Contains(result.OK, []byte(marker)) {
					inner["stored "+marker]++
				}
			}
			var outcomes []struct {
				OK    json.RawMessage `json:"ok"`
				Undef bool            `json:"undef"`
				Err   string          `json:"err"`
			}
			if json.Unmarshal(result.OK, &outcomes) == nil {
				for _, outcome := range outcomes {
					switch {
					case outcome.Err != "":
						inner["err"]++
						inner[digits.ReplaceAllString(outcome.Err, "N")]++
					case outcome.Undef:
						inner["undef"]++
					default:
						inner["ok"]++
						if bytes.Contains(outcome.OK, []byte(`"count":`)) {
							inner["draft"]++
						}
						for _, marker := range []string{`"status":"pending"`, `"type":"input"`, `"status":"placed"`, `"status":"unanswered"`, `"owner":{"conversationId"`, `"type":"write"`} {
							if bytes.Contains(outcome.OK, []byte(marker)) {
								inner["record "+marker]++
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d failed-Session steps; operation outcomes %v", failedSteps, inner)
	if failedSteps < 2 || failedSteps > 400 || inner["ok"] < 4000 || inner["err"] < 500 || inner["undef"] < 100 || inner[`record "owner":{"conversationId"`] < 50 || inner[`record "status":"pending"`] < 50 || inner[`record "status":"placed"`] < 50 {
		t.Errorf("probes are one-sided: %d failed-Session steps, %v", failedSteps, inner)
	}
	differing, ops, failures := 0, map[string]int{}, 0
	for i, program := range programs {
		got := runPigProgram(t, program, storage.NewMemoryStorage(), false)
		differing += compareOracleResults(t, fmt.Sprintf("program %d", i), program, got, expected[i], differing, ops, &failures)
	}
	if differing > 8 {
		t.Errorf("%d answers differ from Pi", differing)
	}
	t.Logf("steps by op %v, %d failed commits", ops, failures)
	if failures < 30 || ops["tx"] < 1500 {
		t.Errorf("probes are one-sided: %v, %d failures", ops, failures)
	}
}

// runPiNode feeds a memory-mode payload (an array of programs) to the Pi driver.
func runPiNode(t *testing.T, mode string, payload []byte) [][]oracleResult {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/memory_oracle.mjs", filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages"), mode)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var results [][]oracleResult
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	return results
}
