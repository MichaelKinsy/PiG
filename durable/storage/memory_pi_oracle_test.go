package storage_test

// pi: packages/durable/src/storage/memory.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// oracleStep is one storage call of a seeded program. Writes, queries and cursors are Pi-shaped JSON, so the same step feeds Pi's MemoryStorage and Pig's.
type oracleStep struct {
	Op             string            `json:"op"`
	Writes         []json.RawMessage `json:"writes"`
	ID             int64             `json:"id,omitempty"`
	ConversationID int64             `json:"conversationId,omitempty"`
	AtOrBefore     *int64            `json:"atOrBefore,omitempty"`
	Query          json.RawMessage   `json:"query,omitempty"`
	Limit          int               `json:"limit,omitempty"`
	CursorFrom     *int              `json:"cursorFrom,omitempty"`
	Cursor         json.RawMessage   `json:"cursor,omitempty"`
	RequestID      string            `json:"requestId"`
	At             json.RawMessage   `json:"at,omitempty"`
	Ops            []json.RawMessage `json:"ops,omitempty"`
	Fail           bool              `json:"fail,omitempty"`
	Scope          *oracleScope      `json:"scope,omitempty"`
}

type oracleProgram struct {
	Steps []oracleStep `json:"steps"`
}

type oracleResult struct {
	Index int             `json:"index"`
	OK    json.RawMessage `json:"ok"`
	Undef bool            `json:"undef"`
	Err   *struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"err"`
}

type oracleGen struct {
	random  *rand.Rand
	nextID  int64
	convs   []int64
	entries []int64 // all entry ids, in write order
	byConv  map[int64][]int64
	scans   []int
	tasks   []int64
	phase   map[int64]int // 0 live, 1 completing, 2 terminal: a Session never moves a task back
	// monotonicTasks keeps each task moving forward through phase. Pi's JsonlStorage cannot reopen a directory where a task moved back to a live state; MemoryStorage has no such limit, so its programs keep backward moves and the status-index moves they make.
	monotonicTasks bool
	subs           []int64
	docs           []oracleDoc
	seqs           int                // commits so far, an upper bound of the stamped sequences
	keys           []oracleRequestKey // request keys submissions were written with
}

type oracleRequestKey struct {
	conversationID int64
	requestID      string
}

// oracleDoc is what the generator remembers of a document it created, so later writes and reads target real incarnations.
type oracleDoc struct {
	id      int64
	kind    string
	key     string // "" for a singleton
	scope   string // the scope member as JSON
	history string
	fork    string
	version int
}

func (g *oracleGen) pick(options ...string) string { return options[g.random.Intn(len(options))] }

func (g *oracleGen) fresh() int64 {
	g.nextID += 1 + int64(g.random.Intn(2))
	return g.nextID
}

func (g *oracleGen) conv() int64 {
	if g.random.Intn(12) == 0 {
		return 1_000_000_000 + int64(g.random.Intn(5)) // never allocated
	}
	return g.convs[g.random.Intn(len(g.convs))]
}

func (g *oracleGen) entry() int64 {
	if len(g.entries) == 0 || g.random.Intn(10) == 0 {
		return 1_000_000_100 + int64(g.random.Intn(5))
	}
	return g.entries[g.random.Intn(len(g.entries))]
}

// data omits JSON null: Pi keeps an entry's present `data: null` while durable.EntryRecord.Data (an any) cannot tell null from absent, so Pig drops it (see questions/batch-ctor.md, "durable EntryRecord data null").
// data leaves out JSON null (Pi keeps a present data:null, Pig drops it: patch with dcore-history). -0 and <, >, & and U+2028 in strings and keys check that the JSONL files are written as JSON.stringify writes them.
func (g *oracleGen) data() string {
	return g.pick(``, ``, `"text"`, `0`, `-0`, `1.5`, `true`, `[]`, `{}`, `{"a":[1,{"b":null}],"c":"d"}`, `[null,"x"]`, `"é😀"`, `1e21`, `{"<k>":"a&b\u2028"}`, `[-0,"</x>"]`)
}

func (g *oracleGen) writeConversation(id int64) string {
	out := fmt.Sprintf(`{"type":"conversation","value":{"id":%d`, id)
	// A conversation never descends from itself: Pi's MemoryStorage accepts a parent cycle and its scans then never end, which no Session produces.
	// The parent is an existing conversation: Pi's MemoryStorage accepts an unknown parent but its ancestry reads then throw a TypeError.
	if parent := g.convs[g.random.Intn(len(g.convs))]; g.random.Intn(2) == 0 && parent != id {
		out += fmt.Sprintf(`,"parent":{"conversationId":%d,"at":%d}`, parent, g.entry())
	}
	// The owner edge feeds the ownerConversationId and ownerTaskId indexes; an owner conversation may be unknown, since Pi does not check it.
	if g.random.Intn(2) == 0 {
		out += fmt.Sprintf(`,"owner":{"conversationId":%d,"taskId":%d}`, g.conv(), g.task())
	}
	return out + `}}`
}

// task is a task ID from a small pool, so owner and byTaskId values repeat.
func (g *oracleGen) task() int64 { return 2_000_000_000 + int64(g.random.Intn(4)) }

// messages is a model or edit message list, sometimes empty: Pi keeps a present empty list.
func (g *oracleGen) messages() string {
	return g.pick(`[]`, `[{"role":"user","content":"hi","timestamp":1}]`,
		`[{"role":"user","content":[{"type":"text","text":"a"}],"timestamp":2},{"role":"assistant","content":[{"type":"text","text":"b"}],"api":"faux:1","provider":"faux","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":3}]`)
}

func (g *oracleGen) writeEntry(id int64, conversationID int64) string {
	out := fmt.Sprintf(`{"type":"entry","value":{"id":%d,"conversationId":%d,"kind":%q`, id, conversationID, g.pick("message", "note", "marker", "x"))
	// Members follow the record's field order (id, conversationId, kind, model, data, head, edits, byTaskId), so the JSONL differential can compare main.jsonl lines byte for byte. The random draws keep their earlier order (data, head, model), so the seeded programs stay the same.
	data, head := g.data(), ""
	switch g.random.Intn(4) {
	case 0:
		head = fmt.Sprintf(`,"head":%d`, g.entry())
	case 1:
		head = fmt.Sprintf(`,"head":%d`, id)
	}
	if g.random.Intn(3) == 0 {
		out += `,"model":` + g.messages()
	}
	if data != "" {
		out += `,"data":` + data
	}
	out += head
	switch g.random.Intn(5) {
	case 0:
		out += fmt.Sprintf(`,"edits":[{"target":%d,"action":"omit"}]`, g.entry())
	case 1:
		out += fmt.Sprintf(`,"edits":[{"target":%d,"action":"replace","messages":%s},{"target":%d,"action":"omit"}]`, g.entry(), g.messages(), g.entry())
	case 2:
		out += `,"edits":[]`
	}
	if g.random.Intn(3) == 0 {
		out += fmt.Sprintf(`,"byTaskId":%d`, g.task())
	}
	return out + `}}`
}

func (g *oracleGen) json() string {
	return g.pick(`null`, `"s"`, `0`, `7`, `true`, `[]`, `{}`, `{"k":[1,null]}`, `"é"`)
}

// checkpoint is never null: Pi keeps a present `checkpoint: null` where Pig's TaskState.Checkpoint (*any) decodes JSON null to a nil pointer (posted to dcore-history with the entry data:null patch).
func (g *oracleGen) checkpoint() string {
	return g.pick(`"s"`, `0`, `7`, `true`, `[]`, `{}`, `{"k":[1,null]}`, `"é"`)
}

func (g *oracleGen) taskID() int64 {
	if len(g.tasks) == 0 || g.random.Intn(10) == 0 {
		return 1_000_000_200 + int64(g.random.Intn(5))
	}
	return g.tasks[g.random.Intn(len(g.tasks))]
}

// outcome never carries `error.detail: null`, and submissionWrite never `detail: null`: Pi keeps a present null detail where Pig's TaskOutcomeError.Detail and SubmissionRecord.Detail (any, omitempty) drop it (55 answers differ with null in; handed to dcore-history with the checkpoint:null note).
func (g *oracleGen) outcome() string {
	return g.pick(`{"status":"completed","result":`+g.json()+`}`, `{"status":"failed","error":{"message":"boom"}}`, `{"status":"failed","error":{"message":"m","detail":{"a":1}},"result":1}`,
		`{"status":"aborted"}`, `{"status":"aborted","reason":"r"}`, `{"status":"orphaned","reason":"gone"}`, `{"status":"faulted","error":{"message":"f"}}`)
}

func (g *oracleGen) taskWrite(id int64) string {
	out := fmt.Sprintf(`{"type":"task","value":{"id":%d,"conversationId":%d,"kind":%q,"version":%d,"input":%s,"background":%v,"abortRequested":%v`,
		id, g.conv(), g.pick("a", "b", "c"), 1+g.random.Intn(3), g.json(), g.random.Intn(2) == 0, g.random.Intn(3) == 0)
	if g.random.Intn(4) == 0 {
		out += fmt.Sprintf(`,"owner":%d`, g.taskID())
	}
	if g.random.Intn(3) == 0 {
		out += fmt.Sprintf(`,"startedAt":%d`, 1_700_000_000_000+g.random.Intn(1000))
	}
	choice := g.random.Intn(5)
	phase := g.phase[id]
	if !g.monotonicTasks {
		phase = 0
	}
	switch phase {
	case 1:
		choice = 3 + g.random.Intn(2)
	case 2:
		choice = 4
	}
	switch choice {
	case 3:
		g.phase[id] = 1
	case 4:
		g.phase[id] = 2
	}
	switch choice {
	case 0:
		out += `,"state":{"status":"pending","checkpoint":` + g.checkpoint() + `}`
	case 1:
		out += `,"state":{"status":"running","checkpoint":` + g.checkpoint() + `}`
	case 2:
		out += fmt.Sprintf(`,"state":{"status":"waiting","checkpoint":%s,"on":[%d],"policy":%q}`, g.checkpoint(), g.taskID(), g.pick("failFast", "allSettled"))
	case 3:
		out += `,"state":{"status":"completing","outcome":` + g.outcome() + `}`
	default:
		out += fmt.Sprintf(`,"endedAt":%d,"state":{"status":"terminal","outcome":%s}`, 1_700_000_001_000+g.random.Intn(1000), g.outcome())
	}
	if g.random.Intn(5) == 0 && strings.Contains(out, `"status":"pending"`) {
		out += `,"memos":{"m":` + g.json() + `}`
	}
	return out + `}}`
}

// submissionConv is one of the first two conversations, now and then an unknown one, which Pi's MemoryStorage does not check.
func (g *oracleGen) submissionConv() int64 {
	if g.random.Intn(12) == 0 {
		return 1_000_000_000 + int64(g.random.Intn(5))
	}
	return g.convs[g.random.Intn(min(2, len(g.convs)))]
}

func (g *oracleGen) submissionWrite(id int64) string {
	// Few conversations and request ids, so submissions collide on a request key and replace one another.
	conversationID := g.submissionConv()
	out := fmt.Sprintf(`{"type":"submission","value":{"id":%d,"conversationId":%d`, id, conversationID)
	if g.random.Intn(2) == 0 {
		requestID := g.pick("r1", "r2", "")
		out += fmt.Sprintf(`,"requestId":%q`, requestID)
		g.keys = append(g.keys, oracleRequestKey{conversationID, requestID})
	}
	kind := g.pick("input", "write")
	out += fmt.Sprintf(`,"type":%q`, kind)
	switch g.random.Intn(4) {
	case 0:
		out += `,"status":"queued"`
	case 1:
		if kind == "input" {
			out += fmt.Sprintf(`,"status":"placed","entry":%d`, g.entry())
		} else {
			out += `,"status":"queued"`
		}
	case 2:
		out += fmt.Sprintf(`,"status":"done","entry":%d`, g.entry())
		if kind == "input" {
			out += fmt.Sprintf(`,"answer":%d`, g.entry())
		}
	default:
		out += `,"status":"unanswered","reason":"why"`
		if g.random.Intn(2) == 0 {
			out += `,"detail":{"d":[1]}`
		}
		if kind == "input" && g.random.Intn(2) == 0 {
			out += fmt.Sprintf(`,"entry":%d`, g.entry())
		}
	}
	return out + `}}`
}

func (g *oracleGen) docValue() string {
	return g.pick(`{}`, `{"a":1}`, `{"a":1,"b":{"c":[1,2]}}`, `{"list":["x"],"s":"t"}`, `{"z":null,"y":false}`)
}

func (g *oracleGen) docOps() string {
	return g.pick(`[["s",["a"],2]]`, `[["s",["b","c"],"x"]]`, `[["d",["a"]]]`, `[["s",["n"],{"k":1}],["s",["a"],3]]`, `[["a",["s"],"more"]]`, `[["r",{"a":9}]]`, `[]`, `[["p",["list"],1,0,["y"]]]`, `[["s",["a"],1]]`)
}

func (g *oracleGen) docScope() (scope, history, fork string) {
	switch g.random.Intn(3) {
	case 0:
		return `{"kind":"session"}`, "", ""
	case 1:
		return fmt.Sprintf(`{"kind":"task","taskId":%d}`, g.taskID()), "", ""
	}
	scope = fmt.Sprintf(`{"kind":"conversation","conversationId":%d}`, g.convs[g.random.Intn(len(g.convs))])
	if g.random.Intn(2) == 0 {
		return scope, "latest", g.pick("current", "initial")
	}
	return scope, "rewindable", g.pick("asOf", "current", "initial")
}

func docRecordJSON(id int64, kind, key, scope, history, fork string) string {
	out := fmt.Sprintf(`{"id":%d,"kind":%q`, id, kind)
	if key != "" {
		out += fmt.Sprintf(`,"key":%q`, key)
	}
	out += `,"scope":` + scope
	if history != "" {
		out += fmt.Sprintf(`,"history":%q,"fork":%q`, history, fork)
	}
	return out + `}`
}

func (g *oracleGen) existingDoc() (oracleDoc, bool) {
	if len(g.docs) == 0 {
		return oracleDoc{}, false
	}
	return g.docs[g.random.Intn(len(g.docs))], true
}

func (g *oracleGen) point() string {
	if g.random.Intn(2) == 0 {
		return `"current"`
	}
	return fmt.Sprint(1 + g.random.Intn(g.seqs+2))
}

func (g *oracleGen) docWrites() []string {
	var writes []string
	count := 1
	if g.random.Intn(4) == 0 {
		count += 1 + g.random.Intn(2)
	}
	for range count {
		doc, found := g.existingDoc()
		switch g.random.Intn(9) {
		case 0, 1, 2: // create
			scope, history, fork := g.docScope()
			created := oracleDoc{id: g.fresh(), kind: g.pick("k1", "k2"), key: g.pick("", "", "a", "b"), scope: scope, history: history, fork: fork, version: 1}
			g.docs = append(g.docs, created)
			writes = append(writes, fmt.Sprintf(`{"type":"document.create","record":%s,"content":{"version":1,"kind":"base","value":%s}}`, docRecordJSON(created.id, created.kind, created.key, scope, history, fork), g.docValue()))
		case 3: // copy
			if !found {
				continue
			}
			scope := fmt.Sprintf(`{"kind":"conversation","conversationId":%d}`, g.convs[g.random.Intn(len(g.convs))])
			created := oracleDoc{id: g.fresh(), kind: doc.kind, key: doc.key, scope: scope, history: doc.history, fork: doc.fork, version: doc.version}
			history, fork := doc.history, doc.fork
			switch g.random.Intn(8) { // a copy that does not match its source is rejected
			case 0:
				created.kind = "k2"
			case 1:
				created.key = g.pick("", "a", "b")
			case 2:
				history, fork = g.pick("latest", "rewindable"), g.pick("current", "initial", "asOf")
			}
			g.docs = append(g.docs, created)
			writes = append(writes, fmt.Sprintf(`{"type":"document.copy","record":%s,"source":{"id":%d,"at":%s}}`, docRecordJSON(created.id, created.kind, created.key, scope, history, fork), doc.id, g.point()))
		case 4, 5: // delta change
			if !found {
				continue
			}
			version := doc.version
			if g.random.Intn(8) == 0 {
				version++
			}
			writes = append(writes, fmt.Sprintf(`{"type":"document.change","id":%d,"content":{"version":%d,"kind":"delta","ops":%s}}`, doc.id, version, g.docOps()))
		case 6: // base change, sometimes a new version
			if !found {
				continue
			}
			version := doc.version
			if g.random.Intn(3) == 0 {
				version++
				for i := range g.docs {
					if g.docs[i].id == doc.id {
						g.docs[i].version = version
					}
				}
			}
			writes = append(writes, fmt.Sprintf(`{"type":"document.change","id":%d,"content":{"version":%d,"kind":"base","value":%s}}`, doc.id, version, g.docValue()))
		case 7: // retire
			if !found {
				continue
			}
			writes = append(writes, fmt.Sprintf(`{"type":"document.retire","id":%d}`, doc.id))
		default: // replace: retire an incarnation and create the next one at its address in one batch, sometimes without the retire
			if !found {
				continue
			}
			created := doc
			created.id, created.version = g.fresh(), 1
			g.docs = append(g.docs, created)
			if g.random.Intn(4) != 0 {
				writes = append(writes, fmt.Sprintf(`{"type":"document.retire","id":%d}`, doc.id))
			}
			writes = append(writes, fmt.Sprintf(`{"type":"document.create","record":%s,"content":{"version":1,"kind":"base","value":%s}}`, docRecordJSON(created.id, created.kind, created.key, created.scope, created.history, created.fork), g.docValue()))
		}
	}
	return writes
}

func (g *oracleGen) docQuery() (json.RawMessage, int) {
	scope, _, _ := g.docScope()
	if doc, found := g.existingDoc(); found && g.random.Intn(10) < 7 {
		scope = doc.scope
	}
	query := `{"scope":` + scope + `,"at":` + g.point()
	if g.random.Intn(3) == 0 {
		query += fmt.Sprintf(`,"kind":%q`, g.pick("k1", "k2"))
	}
	return json.RawMessage(query + `}`), []int{1, 2, 5, 100}[g.random.Intn(4)]
}

func (g *oracleGen) commit() oracleStep {
	var writes []string
	switch g.random.Intn(12) {
	case 10, 11: // documents
		writes = g.docWrites()
		if len(writes) == 0 {
			writes = append(writes, g.writeEntry(g.fresh(), g.conv()))
		}
	case 6, 7: // tasks: new ones, replacements of earlier ones, and IDs another table holds
		for range 1 + g.random.Intn(2) {
			id := g.fresh()
			if foreign, ok := g.foreignID(g.subs); ok {
				id = foreign
			} else if len(g.tasks) > 0 && g.random.Intn(2) == 0 {
				id = g.tasks[g.random.Intn(len(g.tasks))]
			} else {
				g.tasks = append(g.tasks, id)
			}
			writes = append(writes, g.taskWrite(id))
		}
		writes = g.claimTwice(writes, g.submissionWrite)
	case 8, 9: // submissions: new ones, status changes of earlier ones, and IDs another table holds
		for range 1 + g.random.Intn(2) {
			id := g.fresh()
			if foreign, ok := g.foreignID(g.tasks); ok {
				id = foreign
			} else if len(g.subs) > 0 && g.random.Intn(2) == 0 {
				id = g.subs[g.random.Intn(len(g.subs))]
			} else {
				g.subs = append(g.subs, id)
			}
			writes = append(writes, g.submissionWrite(id))
		}
		writes = g.claimTwice(writes, g.taskWrite)
	case 0: // a conversation, sometimes a duplicate id
		id := g.fresh()
		if g.random.Intn(6) == 0 {
			id = g.conv()
		}
		if id == g.convs[0] || slices.Contains(g.convs, id) {
			writes = append(writes, fmt.Sprintf(`{"type":"conversation","value":{"id":%d}}`, id))
		} else {
			writes = append(writes, g.writeConversation(id))
			g.convs = append(g.convs, id)
		}
	case 1: // a conversation with its first entries in one batch
		id := g.fresh()
		writes = append(writes, g.writeConversation(id))
		g.convs = append(g.convs, id)
		for range 1 + g.random.Intn(2) {
			entryID := g.fresh()
			writes = append(writes, g.writeEntry(entryID, id))
			g.entries = append(g.entries, entryID)
			g.byConv[id] = append(g.byConv[id], entryID)
		}
	default: // entries into existing (sometimes unknown) conversations
		for range 1 + g.random.Intn(3) {
			id := g.fresh()
			if g.random.Intn(8) == 0 && len(g.entries) > 0 {
				id = g.entry()
			}
			target := g.conv()
			writes = append(writes, g.writeEntry(id, target))
			g.entries = append(g.entries, id)
			g.byConv[target] = append(g.byConv[target], id)
		}
		if g.random.Intn(8) == 0 {
			writes = append(writes, writes[0])
		}
	}
	g.seqs++
	step := oracleStep{Op: "commit"}
	for _, write := range writes {
		step.Writes = append(step.Writes, json.RawMessage(write))
	}
	return step
}

// foreignID sometimes returns an ID the other record table, a conversation or an entry already holds, so a task or submission write meets Pi's "already belongs to" check.
func (g *oracleGen) foreignID(other []int64) (int64, bool) {
	if g.random.Intn(10) != 0 {
		return 0, false
	}
	pool := slices.Concat(other, g.convs, g.entries)
	return pool[g.random.Intn(len(pool))], true
}

// claimTwice sometimes writes the batch's first ID again as another record type (or an entry), so the batch meets Pi's in-batch "two record types" and "written more than once" checks.
func (g *oracleGen) claimTwice(writes []string, other func(int64) string) []string {
	if g.random.Intn(10) != 0 {
		return writes
	}
	var head struct {
		Value struct {
			ID int64 `json:"id"`
		} `json:"value"`
	}
	if err := json.Unmarshal([]byte(writes[0]), &head); err != nil {
		panic(err)
	}
	if g.random.Intn(2) == 0 {
		return append(writes, g.writeEntry(head.Value.ID, g.conv()))
	}
	return append(writes, other(head.Value.ID))
}

func (g *oracleGen) pick1(ids []int64) int64 {
	if len(ids) == 0 || g.random.Intn(8) == 0 {
		return 1_000_000_300
	}
	return ids[g.random.Intn(len(ids))]
}

func (g *oracleGen) taskQuery(tasks bool) (json.RawMessage, int) {
	limits := []int{1, 1, 2, 3, 5, 100}
	var parts []string
	if g.random.Intn(3) == 0 {
		parts = append(parts, fmt.Sprintf(`"conversationId":%d`, g.conv()))
	}
	if tasks {
		if g.random.Intn(4) == 0 {
			parts = append(parts, fmt.Sprintf(`"kind":%q`, g.pick("a", "b", "c")))
		}
		if g.random.Intn(4) == 0 {
			parts = append(parts, fmt.Sprintf(`"status":%q`, g.pick("pending", "running", "waiting", "completing", "terminal")))
		}
		if g.random.Intn(4) == 0 {
			parts = append(parts, fmt.Sprintf(`"abortRequested":%v`, g.random.Intn(2) == 0))
		}
		if g.random.Intn(4) == 0 {
			parts = append(parts, fmt.Sprintf(`"background":%v`, g.random.Intn(2) == 0))
		}
	} else if g.random.Intn(2) == 0 {
		parts = append(parts, fmt.Sprintf(`"status":%q`, g.pick("queued", "placed", "done", "unanswered")))
	}
	if order := g.pick(``, ``, `"order":"ascending"`, `"order":"descending"`); order != "" {
		parts = append(parts, order)
	}
	return json.RawMessage(`{` + strings.Join(parts, ",") + `}`), limits[g.random.Intn(len(limits))]
}

func (g *oracleGen) scanQuery(entries bool) (json.RawMessage, int) {
	limits := []int{1, 1, 2, 3, 5, 100}
	limit := limits[g.random.Intn(len(limits))]
	order := g.pick(``, ``, `"order":"ascending"`, `"order":"descending"`, ``, ``, `"order":"ascending"`, `"order":"descending"`, `"order":"sideways"`)
	var parts []string
	if entries {
		parts = append(parts, fmt.Sprintf(`"conversationId":%d`, g.conv()))
		if g.random.Intn(3) == 0 {
			parts = append(parts, fmt.Sprintf(`"minEntryId":%d`, g.entry()))
		}
		if g.random.Intn(3) == 0 {
			parts = append(parts, fmt.Sprintf(`"maxEntryId":%d`, g.entry()))
		}
	} else {
		if g.random.Intn(3) == 0 {
			parts = append(parts, fmt.Sprintf(`"ownerConversationId":%d`, g.conv()))
		}
		if g.random.Intn(3) == 0 {
			parts = append(parts, fmt.Sprintf(`"ownerTaskId":%d`, g.task()))
		}
	}
	if order != "" {
		parts = append(parts, order)
	}
	return json.RawMessage(`{` + strings.Join(parts, ",") + `}`), limit
}

func oracleProgramFor(seed int64, monotonicTasks bool) oracleProgram {
	g := &oracleGen{random: rand.New(rand.NewSource(seed)), nextID: 1, convs: []int64{1}, byConv: map[int64][]int64{}, phase: map[int64]int{}, monotonicTasks: monotonicTasks}
	// Every Session starts by committing the root conversation.
	program := oracleProgram{Steps: []oracleStep{{Op: "commit", Writes: []json.RawMessage{json.RawMessage(`{"type":"conversation","value":{"id":1}}`)}}}}
	for range 32 {
		var step oracleStep
		switch g.random.Intn(16) {
		case 0, 1, 2, 3, 8, 9, 12, 13:
			step = g.commit()
		case 4:
			step = oracleStep{Op: g.pick("conversation", "entry", "mintId"), ID: g.conv()}
			if step.Op == "entry" {
				step.ID = g.entry()
			}
		case 5:
			step = oracleStep{Op: "visibleEntry", ConversationID: g.conv(), ID: g.entry()}
		case 6:
			step = oracleStep{Op: "findLatestHeadMarker", ConversationID: g.conv()}
			if g.random.Intn(2) == 0 {
				at := g.entry()
				step.AtOrBefore = &at
			}
		case 10, 11:
			switch g.random.Intn(3) {
			case 0:
				doc, _ := g.existingDoc()
				step = oracleStep{Op: "document", ID: doc.id, At: json.RawMessage(g.point())}
				if len(g.docs) == 0 {
					step.ID = 1_000_000_400
				}
			case 1:
				doc, found := g.existingDoc()
				scope, _, _ := g.docScope()
				if found && g.random.Intn(3) != 0 {
					scope = doc.scope
				}
				address := fmt.Sprintf(`{"kind":%q,"scope":%s`, g.pick("k1", "k2"), scope)
				if found && g.random.Intn(3) != 0 {
					address = fmt.Sprintf(`{"kind":%q,"scope":%s`, doc.kind, doc.scope)
					if doc.key != "" {
						address += fmt.Sprintf(`,"key":%q`, doc.key)
					}
				}
				step = oracleStep{Op: "findDocument", Query: json.RawMessage(address + `}`), At: json.RawMessage(g.point())}
			default:
				step = oracleStep{Op: "scanDocuments"}
				step.Query, step.Limit = g.docQuery()
				if len(g.scans) > 0 && g.random.Intn(2) == 0 {
					from := g.scans[g.random.Intn(len(g.scans))]
					step.CursorFrom = &from
				}
				g.scans = append(g.scans, len(program.Steps))
			}
		case 7:
			switch g.random.Intn(5) {
			case 0:
				step = oracleStep{Op: "task", ID: g.taskID()}
			case 1:
				step = oracleStep{Op: "submission", ID: g.pick1(g.subs)}
			case 2:
				// Mostly a key some submission was written with (it may since have been released or taken over), else any.
				step = oracleStep{Op: "submissionByRequest", ConversationID: g.submissionConv(), RequestID: g.pick("r1", "r2", "", "zz")}
				if len(g.keys) > 0 && g.random.Intn(4) != 0 {
					key := g.keys[g.random.Intn(len(g.keys))]
					step.ConversationID, step.RequestID = key.conversationID, key.requestID
				}
			default:
				step = oracleStep{Op: "scanTasks"}
				if g.random.Intn(2) == 0 {
					step.Op = "scanSubmissions"
				}
				step.Query, step.Limit = g.taskQuery(step.Op == "scanTasks")
				if len(g.scans) > 0 && g.random.Intn(2) == 0 {
					from := g.scans[g.random.Intn(len(g.scans))]
					step.CursorFrom = &from
				}
				g.scans = append(g.scans, len(program.Steps))
			}
		default:
			entries := g.random.Intn(3) != 0
			step = oracleStep{Op: "scanConversations"}
			if entries {
				step.Op = "scanEntries"
			}
			step.Query, step.Limit = g.scanQuery(entries)
			switch {
			case len(g.scans) > 0 && g.random.Intn(2) == 0:
				from := g.scans[g.random.Intn(len(g.scans))]
				step.CursorFrom = &from
			case g.random.Intn(4) == 0:
				// A hand-written cursor: one without an order continues in the scan's default order; the others are invalid. An empty cursor is
				// left out: Pi rejects {} but Go callers pass the zero durable.Cursor for "no cursor", and scan.StartOf documents that both mean the start.
				step.Cursor = json.RawMessage(g.pick(fmt.Sprintf(`{"after":%d}`, g.entry()), fmt.Sprintf(`{"after":%d}`, g.conv()), `{"after":1.5}`, `{"after":"3"}`,
					`{"after":3,"order":"sideways"}`, `{"after":9007199254740992}`, `{"after":-9007199254740991}`, `{"after":3,"order":null}`))
			}
			g.scans = append(g.scans, len(program.Steps))
		}
		program.Steps = append(program.Steps, step)
	}
	return program
}

// settleOracle turns one Pig storage answer into Pi's {ok|undef|err} shape.
func settleOracle(value any, present bool, err error) oracleResult {
	var result oracleResult
	if err != nil {
		name := "Error"
		result.Err = &struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		}{name, err.Error()}
		return result
	}
	if !present {
		result.OK, result.Undef = json.RawMessage("null"), true
		return result
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		panic(marshalErr)
	}
	result.OK = encoded
	return result
}

func pagedOracle[T any](page durable.Page[T, durable.Cursor]) map[string]any {
	items := page.Items
	if items == nil {
		items = []T{}
	}
	out := map[string]any{"items": items}
	if page.Next != nil {
		out["next"] = *page.Next
	}
	return out
}

// runPigProgram runs the steps against one Pig storage; readsOnly skips the commits, as Pi's jsonl-read mode does.
func runPigProgram(t *testing.T, program oracleProgram, memory durable.Storage, readsOnly bool) []oracleResult {
	t.Helper()
	ctx := context.Background()
	next := map[int]durable.Cursor{}
	results := make([]oracleResult, 0, len(program.Steps))
	var tx *pigTx
	defer func() {
		if tx != nil {
			_ = tx.session.Close(ctx)
		}
	}()
	for index, step := range program.Steps {
		if readsOnly && (step.Op == "commit" || step.Op == "tx") {
			continue
		}
		var cursor durable.Cursor
		switch {
		case step.CursorFrom != nil:
			cursor = next[*step.CursorFrom]
		case step.Cursor != nil:
			if err := json.Unmarshal(step.Cursor, &cursor); err != nil {
				t.Fatal(err)
			}
		}
		var result oracleResult
		switch step.Op {
		case "commit":
			writes := make([]durable.StorageWrite, 0, len(step.Writes))
			for _, raw := range step.Writes {
				var head struct {
					Type  string          `json:"type"`
					Value json.RawMessage `json:"value"`
				}
				if err := json.Unmarshal(raw, &head); err != nil {
					t.Fatal(err)
				}
				switch head.Type {
				case "conversation":
					var record durable.ConversationRecord
					if err := json.Unmarshal(head.Value, &record); err != nil {
						t.Fatal(err)
					}
					writes = append(writes, durable.ConversationWrite{Value: record})
				case "document.create", "document.copy", "document.change", "document.retire":
					writes = append(writes, decodeDocumentWrite(t, raw))
				case "task":
					var record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
					if err := json.Unmarshal(head.Value, &record); err != nil {
						t.Fatal(err)
					}
					writes = append(writes, durable.TaskWrite{Value: record})
				case "submission":
					var record durable.SubmissionRecord
					if err := json.Unmarshal(head.Value, &record); err != nil {
						t.Fatal(err)
					}
					writes = append(writes, durable.SubmissionWrite{Value: record})
				default:
					var record durable.EntryRecord
					if err := json.Unmarshal(head.Value, &record); err != nil {
						t.Fatal(err)
					}
					writes = append(writes, durable.EntryWrite{Value: record})
				}
			}
			seq, err := memory.Commit(ctx, writes, func(durable.Seq) {})
			result = settleOracle(seq, true, err)
		case "tx":
			if tx == nil {
				tx = newPigTx(memory)
			}
			result = tx.run(t, step)
		case "mintId":
			id, err := memory.MintId()
			result = settleOracle(id, true, err)
		case "conversation":
			record, err := memory.Conversation(ctx, durable.ConversationId(step.ID))
			result = settleOracle(record, record != nil, err)
		case "entry":
			found, err := memory.Entry(ctx, durable.EntryId(step.ID))
			result = settleOracle(entryAtOracle(found), found != nil, err)
		case "visibleEntry":
			found, err := memory.VisibleEntry(ctx, durable.ConversationId(step.ConversationID), durable.EntryId(step.ID))
			result = settleOracle(entryAtOracle(found), found != nil, err)
		case "findLatestHeadMarker":
			var at *durable.EntryId
			if step.AtOrBefore != nil {
				at = new(durable.EntryId(*step.AtOrBefore))
			}
			record, err := memory.FindLatestHeadMarker(ctx, durable.ConversationId(step.ConversationID), at)
			result = settleOracle(record, record != nil, err)
		case "document":
			stored, err := memory.Document(ctx, durable.DocumentId(step.ID), oraclePoint(t, step.At))
			result = settleOracle(storedDocumentOracle(stored), stored != nil, err)
		case "findDocument":
			var address durable.DocumentAddress
			if err := json.Unmarshal(step.Query, &address); err != nil {
				t.Fatal(err)
			}
			record, err := memory.FindDocument(ctx, address, oraclePoint(t, step.At))
			result = settleOracle(record, record != nil, err)
		case "scanDocuments":
			var query struct {
				Scope durable.DocumentRecordScope `json:"scope"`
				At    json.RawMessage             `json:"at"`
				Kind  *string                     `json:"kind"`
			}
			if err := json.Unmarshal(step.Query, &query); err != nil {
				t.Fatal(err)
			}
			var named struct {
				Scope struct {
					Ref *int `json:"ref"`
				} `json:"scope"`
			}
			if err := json.Unmarshal(step.Query, &named); err != nil {
				t.Fatal(err)
			}
			if named.Scope.Ref != nil && tx != nil {
				if query.Scope.Kind == durable.ScopeTask {
					query.Scope.TaskId = durable.TaskId(pickID(&txRef{Ref: *named.Scope.Ref}, tx.tasks))
				} else {
					query.Scope.ConversationId = durable.ConversationId(pickID(&txRef{Ref: *named.Scope.Ref}, tx.convs))
				}
			}
			page, err := memory.ScanDocuments(ctx, durable.DocumentQuery{Scope: query.Scope, At: oraclePoint(t, query.At), Kind: query.Kind}, step.Limit, cursor)
			result = settleOracle(pagedOracle(page), true, err)
			if err == nil && page.Next != nil {
				next[index] = *page.Next
			}
		case "task":
			record, err := memory.Task(ctx, durable.TaskId(step.ID))
			result = settleOracle(record, record != nil, err)
		case "submission":
			record, err := memory.Submission(ctx, durable.SubmissionId(step.ID))
			result = settleOracle(record, record != nil, err)
		case "submissionByRequest":
			record, err := memory.SubmissionByRequest(ctx, durable.ConversationId(step.ConversationID), step.RequestID)
			result = settleOracle(record, record != nil, err)
		case "scanTasks":
			var query durable.TaskQuery
			if err := json.Unmarshal(step.Query, &query); err != nil {
				t.Fatal(err)
			}
			page, err := memory.ScanTasks(ctx, query, step.Limit, cursor)
			result = settleOracle(pagedOracle(page), true, err)
			if err == nil && page.Next != nil {
				next[index] = *page.Next
			}
		case "scanSubmissions":
			var query durable.SubmissionQuery
			if err := json.Unmarshal(step.Query, &query); err != nil {
				t.Fatal(err)
			}
			page, err := memory.ScanSubmissions(ctx, query, step.Limit, cursor)
			result = settleOracle(pagedOracle(page), true, err)
			if err == nil && page.Next != nil {
				next[index] = *page.Next
			}
		case "scanConversations":
			var query durable.ConversationQuery
			if err := json.Unmarshal(step.Query, &query); err != nil {
				t.Fatal(err)
			}
			page, err := memory.ScanConversations(ctx, query, step.Limit, cursor)
			result = settleOracle(pagedOracle(page), true, err)
			if err == nil && page.Next != nil {
				next[index] = *page.Next
			}
		case "scanEntries":
			var query durable.EntryQuery
			if err := json.Unmarshal(step.Query, &query); err != nil {
				t.Fatal(err)
			}
			page, err := memory.ScanEntries(ctx, query, step.Limit, cursor)
			result = settleOracle(pagedOracle(page), true, err)
			if err == nil && page.Next != nil {
				next[index] = *page.Next
			}
		}
		result.Index = index
		results = append(results, result)
	}
	return results
}

func entryAtOracle(found *durable.EntryAt) any {
	if found == nil {
		return nil
	}
	return map[string]any{"entry": found.Entry, "commitSeq": found.CommitSeq}
}

func canonicalOracleJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var value any
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return value
}

// Pi's MemoryStorage runs in Node over the same seeded programs (commits that succeed and ones it rejects, point reads, head markers, ordered scans with cursors) and every answer must match: values, absence and the rejection text.
func TestMemoryStorageMatchesPiOnSeededPrograms(t *testing.T) {
	const seededPrograms = 800
	programs := make([]oracleProgram, seededPrograms)
	for i := range programs {
		programs[i] = oracleProgramFor(int64(20261010+i), false)
	}
	programs = append(programs, requestKeyPrograms()...)
	programs = append(programs, documentCopyPrograms()...)
	programs = append(programs, danglingParentPrograms()...)
	programs = append(programs, statusPrecedencePrograms()...)
	programs = append(programs, documentBoundaryPrograms()...)
	programs = append(programs, indexedStringPrograms()...)
	payload, err := json.Marshal(programs)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/memory_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]oracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(programs) {
		t.Fatalf("Pi answered %d of %d programs", len(expected), len(programs))
	}
	differing, ops, found, failures := 0, map[string]int{}, map[string]int{}, 0
	for i, program := range programs {
		got := runPigProgram(t, program, storage.NewMemoryStorage(), false)
		differing += compareOracleResults(t, fmt.Sprintf("program %d", i), program, got, expected[i], differing, ops, &failures)
		for _, want := range expected[i] {
			step := program.Steps[want.Index]
			if want.Err != nil {
				if step.Op == "commit" && strings.HasPrefix(want.Err.Message, "ID ") && slices.ContainsFunc(step.Writes, isTaskOrSubmissionWrite) {
					found["task/submission ID clash"]++
				}
			} else if !want.Undef && !bytes.HasPrefix(want.OK, []byte(`{"items":[]`)) {
				found[step.Op]++
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d answers differ from Pi", differing)
	}
	t.Logf("steps by op %v, %d rejected; answers with a value or items %v", ops, failures, found)
	if failures < 100 || ops["scanEntries"] < 200 || ops["commit"] < 400 || ops["document"] < 200 || ops["findDocument"] < 200 || ops["scanDocuments"] < 200 {
		t.Errorf("probes are one-sided: %v, %d rejections", ops, failures)
	}
	// Agreeing on absence and empty pages proves nothing: each task, submission and document read must also find records.
	for _, op := range []string{"task", "submission", "submissionByRequest", "scanTasks", "scanSubmissions", "task/submission ID clash", "document", "findDocument", "scanDocuments"} {
		if found[op] < 50 {
			t.Errorf("only %d %s answers carry a record: %v", found[op], op, found)
		}
	}
}

// compareOracleResults compares Pig's answers with Pi's, matched by step index, and returns how many differ; it reports the first few (the caller passes how many were already reported).
func compareOracleResults(t *testing.T, label string, program oracleProgram, got, want []oracleResult, reported int, ops map[string]int, failures *int) int {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: Pig answered %d steps, Pi %d", label, len(got), len(want))
		return 1
	}
	differing := 0
	for k := range want {
		j := want[k].Index
		if j < 0 || got[k].Index < 0 { // the storage did not open
			if got[k].Index != j || got[k].Err == nil || want[k].Err == nil || got[k].Err.Message != want[k].Err.Message || got[k].Err.Name != want[k].Err.Name {
				if differing++; reported+differing <= 8 {
					t.Errorf("%s: opening the directory: Pig %+v, Pi %+v", label, got[k].Err, want[k].Err)
				}
			}
			continue
		}
		step := program.Steps[j]
		if got[k].Index != j {
			t.Errorf("%s: answers out of step order (%d vs %d)", label, got[k].Index, j)
			return differing + 1
		}
		ops[step.Op]++
		if want[k].Err != nil {
			*failures++
		}
		same := (got[k].Err == nil) == (want[k].Err == nil) && got[k].Undef == want[k].Undef
		if same && want[k].Err != nil {
			same = got[k].Err.Message == want[k].Err.Message
		}
		if same && want[k].Err == nil && !want[k].Undef {
			same = reflect.DeepEqual(canonicalOracleJSON(t, got[k].OK), canonicalOracleJSON(t, want[k].OK))
			if same && step.Op == "document" {
				// A materialized document keeps its members in insertion order, like JSON.parse and JSON.stringify.
				same = materializedOrder(t, got[k].OK) == materializedOrder(t, want[k].OK)
			}
		}
		if !same {
			if differing++; reported+differing <= 8 {
				t.Errorf("%s step %d %s\n  step %s\n  Pig %s %v\n  Pi  %s %v", label, j, step.Op, mustMarshalOracle(step), got[k].OK, got[k].Err, want[k].OK, want[k].Err)
			}
		}
	}
	return differing
}

func mustMarshalOracle(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// requestKeyPrograms pin the conversation-scoped request key index: a later submission takes over a key, so rewriting the earlier one must not release it, while rewriting the owner with another or no key, or into another conversation, does.
func requestKeyPrograms() []oracleProgram {
	submission := func(id int, request string, status string) json.RawMessage {
		key := ""
		if request != "" {
			key = fmt.Sprintf(`"requestId":%q,`, request)
		}
		return json.RawMessage(fmt.Sprintf(`{"type":"submission","value":{"id":%d,"conversationId":1,%s"type":"write","status":%q%s}}`, id, key, status, map[bool]string{true: `,"reason":"r"`, false: ``}[status == "unanswered"]))
	}
	commit := func(writes ...json.RawMessage) oracleStep { return oracleStep{Op: "commit", Writes: writes} }
	root := commit(json.RawMessage(`{"type":"conversation","value":{"id":1}}`))
	byRequest := oracleStep{Op: "submissionByRequest", ConversationID: 1, RequestID: "k"}
	return []oracleProgram{
		{Steps: []oracleStep{root, commit(submission(2, "k", "queued")), commit(submission(3, "k", "queued")), byRequest, commit(submission(2, "k", "unanswered")), byRequest, commit(submission(3, "", "unanswered")), byRequest}},
		{Steps: []oracleStep{root, commit(submission(2, "k", "queued")), commit(submission(3, "k", "queued")), commit(submission(2, "", "unanswered")), byRequest, commit(submission(3, "j", "unanswered")), byRequest}},
		// A rewrite into another conversation releases the key in the conversation the submission left.
		{Steps: []oracleStep{root, commit(json.RawMessage(`{"type":"conversation","value":{"id":5}}`)), commit(submission(2, "k", "queued")),
			commit(json.RawMessage(`{"type":"submission","value":{"id":2,"conversationId":5,"requestId":"k","type":"write","status":"queued"}}`)),
			byRequest, {Op: "submissionByRequest", ConversationID: 5, RequestID: "k"}}},
	}
}

func oraclePoint(t *testing.T, raw json.RawMessage) durable.DocumentPoint {
	t.Helper()
	if string(raw) == `"current"` {
		return durable.CurrentPoint
	}
	var seq int64
	if err := json.Unmarshal(raw, &seq); err != nil {
		t.Fatalf("point %s: %v", raw, err)
	}
	return durable.AtSeq(durable.Seq(seq))
}

func storedDocumentOracle(stored *durable.StoredDocument) any {
	if stored == nil {
		return nil
	}
	return map[string]any{"record": stored.Record, "version": stored.Version, "value": stored.Value, "deltasSinceBase": stored.DeltasSinceBase}
}

func decodeDocumentWrite(t *testing.T, raw json.RawMessage) durable.StorageWrite {
	t.Helper()
	var head struct {
		Type    string                  `json:"type"`
		Record  durable.DocumentCreate  `json:"record"`
		Content durable.DocumentContent `json:"content"`
		ID      durable.DocumentId      `json:"id"`
		Source  struct {
			ID durable.DocumentId `json:"id"`
			At json.RawMessage    `json:"at"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	switch head.Type {
	case "document.create":
		return durable.DocumentCreateWrite{Record: head.Record, Content: head.Content}
	case "document.copy":
		return durable.DocumentCopyWrite{Record: head.Record, Source: durable.DocumentCopySource{Id: head.Source.ID, At: oraclePoint(t, head.Source.At)}}
	case "document.change":
		return durable.DocumentChangeWrite{Id: head.ID, Content: head.Content}
	default:
		return durable.DocumentRetireWrite{Id: head.ID}
	}
}

// documentCopyPrograms pin each rule of a definition-free copy: the source is read at the requested point and the copy must keep its kind, key, history and fork.
// Each copy goes to its own conversation (copy ID + 100): a copy at the source's own address is rejected first as a second current incarnation, which would hide every matching rule.
func documentCopyPrograms() []oracleProgram {
	commit := func(writes ...string) oracleStep {
		step := oracleStep{Op: "commit"}
		for _, write := range writes {
			step.Writes = append(step.Writes, json.RawMessage(write))
		}
		return step
	}
	scope := `{"kind":"conversation","conversationId":1}`
	source := fmt.Sprintf(`{"type":"document.create","record":{"id":10,"kind":"k1","key":"a","scope":%s,"history":"rewindable","fork":"asOf"},"content":{"version":1,"kind":"base","value":{"n":1}}}`, scope)
	change := `{"type":"document.change","id":10,"content":{"version":1,"kind":"delta","ops":[["s",["n"],2]]}}`
	copyOf := func(id int, at, kind, key, history, fork string) string {
		// Members in the order Pi's Session builds a copy record (session/forks.ts collectCopies), which JSONL persists as given.
		record := docRecordJSON(int64(id), kind, key, fmt.Sprintf(`{"kind":"conversation","conversationId":%d}`, id+100), history, fork)
		return fmt.Sprintf(`{"type":"document.copy","record":%s,"source":{"id":10,"at":%s}}`, record, at)
	}
	conversations := []string{`{"type":"conversation","value":{"id":1}}`}
	for id := 120; id <= 131; id++ {
		conversations = append(conversations, fmt.Sprintf(`{"type":"conversation","value":{"id":%d}}`, id))
	}
	steps := []oracleStep{commit(conversations...), commit(source), commit(change)}
	for index, write := range []string{
		copyOf(20, `"current"`, "k1", "a", "rewindable", "asOf"), copyOf(21, `2`, "k1", "a", "rewindable", "asOf"), copyOf(22, `3`, "k1", "a", "rewindable", "asOf"), copyOf(23, `"current"`, "k2", "a", "rewindable", "asOf"),
		copyOf(24, `"current"`, "k1", "", "rewindable", "asOf"), copyOf(25, `"current"`, "k1", "a", "latest", "asOf"), copyOf(26, `"current"`, "k1", "a", "rewindable", "current"), copyOf(27, `1`, "k1", "a", "rewindable", "asOf"),
	} {
		steps = append(steps, commit(write), oracleStep{Op: "document", ID: int64(20 + index), At: json.RawMessage(`"current"`)})
	}
	// A copy takes the version of the source at the copied point: 2 after a new-version base, 1 from before it.
	steps = append(steps, commit(`{"type":"document.change","id":10,"content":{"version":2,"kind":"base","value":{"m":1}}}`),
		commit(copyOf(30, `"current"`, "k1", "a", "rewindable", "asOf")), oracleStep{Op: "document", ID: 30, At: json.RawMessage(`"current"`)},
		commit(copyOf(31, `3`, "k1", "a", "rewindable", "asOf")), oracleStep{Op: "document", ID: 31, At: json.RawMessage(`"current"`)})
	// Retiring the current incarnation and creating the next at its address is one valid batch; creating without the retire is not.
	address := json.RawMessage(fmt.Sprintf(`{"kind":"k1","key":"a","scope":%s}`, scope))
	next := strings.Replace(strings.Replace(source, `"id":10`, `"id":40`, 1), `{"n":1}`, `{"n":40}`, 1)
	steps = append(steps, commit(strings.Replace(next, `"id":40`, `"id":41`, 1)),
		commit(`{"type":"document.retire","id":10}`, next), oracleStep{Op: "findDocument", Query: address, At: json.RawMessage(`"current"`)},
		oracleStep{Op: "findDocument", Query: address, At: json.RawMessage(`3`)}, oracleStep{Op: "document", ID: 40, At: json.RawMessage(`"current"`)})
	return []oracleProgram{{Steps: steps}}
}

// danglingParentPrograms read through a conversation whose parent is not stored. A commit does not check the parent, so Pi's lineage walks read `.parent` of undefined and throw a TypeError once they reach it, and not before: entries found earlier, or a scan whose limit fills first, still answer.
func danglingParentPrograms() []oracleProgram {
	commit := func(writes ...string) oracleStep {
		step := oracleStep{Op: "commit"}
		for _, write := range writes {
			step.Writes = append(step.Writes, json.RawMessage(write))
		}
		return step
	}
	scan := func(query string, limit int) oracleStep {
		return oracleStep{Op: "scanEntries", Query: json.RawMessage(query), Limit: limit}
	}
	at := func(id int64) *int64 { return &id }
	steps := []oracleStep{
		commit(`{"type":"conversation","value":{"id":1}}`),
		commit(`{"type":"conversation","value":{"id":2,"parent":{"conversationId":9,"at":5}}}`),
		// Entry 5 is stored under the missing conversation itself, inside the fork point.
		commit(`{"type":"entry","value":{"id":3,"conversationId":2,"kind":"note"}}`, `{"type":"entry","value":{"id":4,"conversationId":2,"kind":"note"}}`, `{"type":"entry","value":{"id":5,"conversationId":9,"kind":"note","head":5}}`),
		{Op: "findLatestHeadMarker", ConversationID: 2},
		{Op: "findLatestHeadMarker", ConversationID: 2, AtOrBefore: at(4)},
		{Op: "visibleEntry", ConversationID: 2, ID: 4},
		{Op: "visibleEntry", ConversationID: 2, ID: 5},
		{Op: "visibleEntry", ConversationID: 2, ID: 1},
		scan(`{"conversationId":2}`, 1),
		scan(`{"conversationId":2}`, 2),
		scan(`{"conversationId":2}`, 3),
		scan(`{"conversationId":2,"minEntryId":4}`, 5),
		scan(`{"conversationId":2,"order":"ascending"}`, 1),
		scan(`{"conversationId":2,"order":"ascending","minEntryId":6}`, 5),
		{Op: "scanConversations", Query: json.RawMessage(`{}`), Limit: 5},
	}
	return []oracleProgram{{Steps: steps}}
}

// statusPrecedencePrograms pin where an unknown task or submission status fails: Pi's apply throws on it only after every prepare check passed, so a document error in the same batch is the one reported, and nothing is stored.
func statusPrecedencePrograms() []oracleProgram {
	commit := func(writes ...string) oracleStep {
		step := oracleStep{Op: "commit"}
		for _, write := range writes {
			step.Writes = append(step.Writes, json.RawMessage(write))
		}
		return step
	}
	task := `{"type":"task","value":{"id":2,"conversationId":1,"kind":"k","version":1,"background":false,"abortRequested":false,"input":null,"state":{"status":"bogus"}}}`
	submission := `{"type":"submission","value":{"id":3,"conversationId":1,"type":"write","status":"bogus"}}`
	return []oracleProgram{{Steps: []oracleStep{
		commit(`{"type":"conversation","value":{"id":1}}`),
		commit(task, `{"type":"document.retire","id":99}`),
		commit(submission, `{"type":"document.change","id":98,"content":{"version":2,"kind":"base","value":{}}}`),
		{Op: "task", ID: 2},
		{Op: "submission", ID: 3},
		{Op: "mintId"},
	}}}
}

// documentBoundaryPrograms read a rewindable document at the exact sequence of each revision, including a base written after deltas: the nearest base at or before the point must win, and only deltas after it apply.
func documentBoundaryPrograms() []oracleProgram {
	scope := `{"kind":"conversation","conversationId":1}`
	commit := func(write string) oracleStep {
		return oracleStep{Op: "commit", Writes: []json.RawMessage{json.RawMessage(write)}}
	}
	steps := []oracleStep{
		commit(`{"type":"conversation","value":{"id":1}}`),
		commit(fmt.Sprintf(`{"type":"document.create","record":{"id":10,"kind":"k1","scope":%s,"history":"rewindable","fork":"asOf"},"content":{"version":1,"kind":"base","value":{"n":1,"m":[1]}}}`, scope)),
		commit(`{"type":"document.change","id":10,"content":{"version":1,"kind":"delta","ops":[["s",["n"],2]]}}`),
		commit(`{"type":"document.change","id":10,"content":{"version":1,"kind":"base","value":{"n":9}}}`),
		commit(`{"type":"document.change","id":10,"content":{"version":1,"kind":"delta","ops":[["s",["k"],true]]}}`),
		commit(`{"type":"document.change","id":10,"content":{"version":2,"kind":"base","value":{"n":10}}}`),
	}
	for _, at := range []string{`1`, `2`, `3`, `4`, `5`, `6`, `7`, `"current"`} {
		steps = append(steps, oracleStep{Op: "document", ID: 10, At: json.RawMessage(at)})
	}
	return []oracleProgram{{Steps: steps}}
}

func materializedOrder(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var stored struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, stored.Value); err != nil {
		t.Fatalf("%s: %v", stored.Value, err)
	}
	return compact.String()
}

// oracleScope binds a tx step like a conversation- or task-scoped runtime commit (Pi's TransactionScope).
type oracleScope struct {
	Conv *txRef `json:"conv,omitempty"`
	Task *txRef `json:"task,omitempty"`
}

func isTaskOrSubmissionWrite(raw json.RawMessage) bool {
	return bytes.HasPrefix(raw, []byte(`{"type":"task"`)) || bytes.HasPrefix(raw, []byte(`{"type":"submission"`))
}

// indexedStringPrograms write a task kind, a request ID and a document kind and key with characters encoding/json escapes (<, >, &, U+2028) and read them back through the indexed lookups: SQLite stores these columns as JSON.stringify text and matches queries against it.
func indexedStringPrograms() []oracleProgram {
	commit := func(write string) oracleStep {
		return oracleStep{Op: "commit", Writes: []json.RawMessage{json.RawMessage(write)}}
	}
	scope := `{"kind":"conversation","conversationId":1}`
	return []oracleProgram{{Steps: []oracleStep{
		commit(`{"type":"conversation","value":{"id":1}}`),
		commit(`{"type":"task","value":{"id":2,"conversationId":1,"kind":"a<b>&\u2028","version":1,"input":null,"background":false,"abortRequested":false,"state":{"status":"pending","checkpoint":0}}}`),
		commit(`{"type":"submission","value":{"id":3,"conversationId":1,"requestId":"r<&>\u2029","type":"write","status":"queued"}}`),
		commit(fmt.Sprintf(`{"type":"document.create","record":{"id":4,"kind":"k<","key":"a&b","scope":%s,"history":"rewindable","fork":"asOf"},"content":{"version":1,"kind":"base","value":{"n":1}}}`, scope)),
		{Op: "scanTasks", Query: json.RawMessage(`{"kind":"a<b>&\u2028"}`), Limit: 5},
		{Op: "submissionByRequest", ConversationID: 1, RequestID: "r<&>\u2029"},
		{Op: "findDocument", Query: json.RawMessage(fmt.Sprintf(`{"kind":"k<","key":"a&b","scope":%s}`, scope)), At: json.RawMessage(`"current"`)},
		{Op: "scanDocuments", Query: json.RawMessage(fmt.Sprintf(`{"scope":%s,"at":"current","kind":"k<"}`, scope)), Limit: 5},
	}}}
}
