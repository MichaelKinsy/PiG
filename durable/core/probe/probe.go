// SPDX-License-Identifier: MIT

// Package probe is a small ABI 1 core that exists to test hosts. It implements no Pi semantics. It exercises every
// host loop invariant of ABI.md section 3 (reads before anything else, commits before effects, the single-writer
// guard, rejected and fatal steps, effect cancellation) and every step field, with deterministic output, so one
// event script drives the native Step and the Wasm binding and the step bytes can be compared exactly.
package probe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
)

// SQL statement IDs of the probe's table, in sql_table order.
const (
	sqlCreateMeta = iota
	sqlCreateLog
	sqlInsertMeta
	sqlListTables
	sqlSelectMeta
	sqlInsertLog
	sqlGuardMeta
	sqlSelectLog
)

// SQL is the probe's sql_table. The durable_metadata statements have the shape the host guard recognises.
var SQL = []string{
	"CREATE TABLE IF NOT EXISTS durable_metadata (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), next_id TEXT NOT NULL, next_seq INTEGER NOT NULL)",
	"CREATE TABLE IF NOT EXISTS probe_log (n INTEGER PRIMARY KEY, note TEXT NOT NULL, at REAL NOT NULL, data BLOB)",
	"INSERT OR IGNORE INTO durable_metadata (singleton, next_id, next_seq) VALUES (1, '1', 1)",
	"SELECT name FROM sqlite_master WHERE type = 'table' AND name IN ('durable_metadata', 'probe_log') ORDER BY name",
	"SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1",
	"INSERT INTO probe_log (n, note, at, data) VALUES (?, ?, ?, ?)",
	"UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1 AND next_seq = ?",
	"SELECT n, note, at, CAST(data AS BLOB) FROM probe_log ORDER BY n",
}

const (
	readTables  = 1
	readMeta    = 2
	readInspect = 3
)

// Session is one probe handle.
type Session struct {
	opened    bool
	closed    bool
	nextSeq   int64
	nextEffID uint32
	pending   []uint32 // effect IDs in start order
	inspectID uint32
	requestID string
	open      payload.OpenOptions
	uuidv7    func() string
	logBytes  uint64
}

// NewSession returns a fresh handle. uuidv7 is the host's pi-ai uuidv7() generator (ABI section 4.2).
func NewSession(uuidv7 func() string) *Session {
	return &Session{nextSeq: 1, nextEffID: 1, uuidv7: uuidv7}
}

func reject(name, message string) *abi.Step {
	//portlint:allow mapkeyorder the rejection body is read by field name; no reader depends on key order
	body, _ := json.Marshal(map[string]string{"name": name, "message": message})
	return &abi.Step{Status: abi.StatusRejected, Err: body}
}

func (s *Session) effect(kind abi.EffectKind, payload []byte) abi.Effect {
	id := s.nextEffID
	s.nextEffID++
	if kind != abi.EffectTimerClear && kind != abi.EffectCancel && kind != abi.EffectLiveness {
		s.pending = append(s.pending, id)
	}
	return abi.Effect{ID: id, Kind: kind, Payload: payload}
}

func (s *Session) complete(id uint32) {
	for i, p := range s.pending {
		if p == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

// commit builds a commit that logs a note and advances next_seq, as every Pi commit does.
func (s *Session) commit(note string, at float64, data []byte, staleGuard bool) abi.Commit {
	seq := s.nextSeq
	s.nextSeq++
	expect := seq
	if staleGuard {
		expect = seq + 1000
	}
	s.logBytes += uint64(len(note) + len(data))
	return abi.Commit{Seq: seq, Stmts: []abi.Stmt{
		{SQL: sqlInsertLog, Params: []abi.Value{abi.Int(seq), abi.Text(note), abi.Float(at), abi.Blob(data)}},
		{SQL: sqlGuardMeta, Params: []abi.Value{abi.Text(fmt.Sprint(seq + 1)), abi.Int(seq + 1), abi.Int(expect)}},
	}}
}

func notice(kind abi.NoticeKind, v any) abi.Notice {
	body, _ := json.Marshal(v)
	return abi.Notice{Kind: kind, Payload: body}
}

func withID(id uint32, body []byte) []byte {
	return append(abi.U32Payload(id), body...)
}

// Step processes one event.
func (s *Session) Step(ev abi.Event) *abi.Step {
	if s.closed {
		return reject("SessionClosed", "the handle is closed")
	}
	if ev.Kind != abi.EventOpen && ev.Kind != abi.EventRows && !s.opened && ev.Kind != abi.EventClose {
		return reject("NotOpen", "open the session first")
	}
	switch ev.Kind {
	case abi.EventOpen:
		if err := json.Unmarshal(ev.Payload, &s.open); err != nil {
			return reject("InvalidOpen", err.Error())
		}
		return &abi.Step{Reads: []abi.Read{{ID: readTables, SQL: sqlListTables}}}
	case abi.EventRows:
		return s.rows(ev)
	case abi.EventSubmit:
		return s.submit(ev)
	case abi.EventModelEvent:
		return s.modelEvent(ev)
	case abi.EventToolDone:
		s.complete(ev.ID)
		note := fmt.Sprintf("tool:%d:%d", ev.ID, ev.Phase)
		step := &abi.Step{Commits: []abi.Commit{s.commit(note, ev.Now, ev.Payload, false)}}
		step.Notices = append(step.Notices, notice(abi.NoticePublished, payload.Published{Submissions: []payload.SubmissionSettlement{{RequestID: s.requestID, Status: "done"}}}))
		step.Effects = append(step.Effects, s.effect(abi.EffectTimerClear, abi.U32Payload(1)), s.effect(abi.EffectLiveness, abi.F64Payload(-1)))
		return step
	case abi.EventTimer:
		return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeReport, map[string]any{"timer": ev.ID, "now": ev.Now})}}
	case abi.EventAbort:
		step := &abi.Step{}
		for _, id := range s.pending {
			step.Effects = append(step.Effects, s.effect(abi.EffectCancel, abi.U32Payload(id)))
		}
		return step
	case abi.EventClose:
		step := &abi.Step{}
		for _, id := range s.pending {
			step.Effects = append(step.Effects, s.effect(abi.EffectCancel, abi.U32Payload(id)))
		}
		s.pending = nil
		s.closed = true
		return step
	case abi.EventAPI:
		return &abi.Step{Notices: []abi.Notice{{Kind: abi.NoticeAPIResult, Payload: withID(ev.ID, append(append([]byte(`{"echo":`), ev.Payload...), '}'))}}}
	case abi.EventTx:
		return &abi.Step{Notices: []abi.Notice{{Kind: abi.NoticeTxResult, Payload: withID(ev.ID, []byte(`{"ready":true}`))}}}
	case abi.EventInspect:
		return s.inspect(ev)
	default: // model_bytes, tool_progress, hook_done, phase_done, registry: accepted, no effect on the probe
		return &abi.Step{}
	}
}

func (s *Session) rows(ev abi.Event) *abi.Step {
	step := &abi.Step{}
	for _, a := range ev.Rows {
		switch a.ID {
		case readTables:
			if len(a.Rows) == 2 {
				step.Reads = append(step.Reads, abi.Read{ID: readMeta, SQL: sqlSelectMeta})
			} else {
				step.Commits = append(step.Commits, abi.Commit{Seq: -1, Stmts: []abi.Stmt{{SQL: sqlCreateMeta}, {SQL: sqlCreateLog}, {SQL: sqlInsertMeta}}})
				s.opened = true
				step.Notices = append(step.Notices, notice(abi.NoticeReport, map[string]any{"opened": true, "created": true, "nextSeq": s.nextSeq, "sidecar": s.open.Sidecar}))
			}
		case readMeta:
			if len(a.Rows) == 1 && len(a.Rows[0]) == 2 {
				s.nextSeq = int64(a.Rows[0][1].Number())
			}
			s.opened = true
			step.Notices = append(step.Notices, notice(abi.NoticeReport, map[string]any{"opened": true, "created": false, "nextSeq": s.nextSeq, "sidecar": s.open.Sidecar}))
		case readInspect:
			var out []string
			for _, row := range a.Rows {
				out = append(out, fmt.Sprintf("%d:%s:%g:%x", int64(row[0].Number()), row[1].Bytes, row[2].Number(), row[3].Bytes))
			}
			step.Notices = append(step.Notices, abi.Notice{Kind: abi.NoticeAPIResult, Payload: withID(s.inspectID, mustJSON(map[string]any{"log": out}))})
		}
	}
	if len(step.Reads) > 0 && len(step.Commits) > 0 {
		step.Commits = nil
	}
	return step
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (s *Session) submit(ev abi.Event) *abi.Step {
	var sub payload.SubmitEvent
	if err := json.Unmarshal(ev.Payload, &sub); err != nil {
		return reject("InvalidSubmission", err.Error())
	}
	var content string
	_ = json.Unmarshal(sub.Content, &content)
	switch content {
	case "reject":
		return reject("InvalidSubmission", "rejected by the probe")
	case "fatal":
		body := mustJSON(map[string]string{"name": "ProbeFatal", "message": "the probe asked the host to discard the handle"})
		return &abi.Step{Status: abi.StatusFatal, Err: body}
	}
	s.requestID = sub.RequestID
	step := &abi.Step{Commits: []abi.Commit{
		s.commit("submit:"+sub.RequestID, ev.Now, []byte(content), content == "stale"),
		s.commit("placed:"+sub.RequestID, ev.Now, nil, false),
	}}
	step.Effects = append(step.Effects, s.effect(abi.EffectTimer, abi.TimerEffect(1, ev.Now+100, true)))
	count := 1
	if content == "multi" {
		count = 3
	}
	for range count {
		ctx := mustJSON(map[string]any{
			"messages": []any{map[string]any{"role": "user", "content": content, "timestamp": int64(ev.Now)}},
		})
		step.Effects = append(step.Effects, s.effect(abi.EffectModelContext, mustJSON(payload.ModelContextEffect{
			Model: payload.ModelRef{Provider: "probe", ModelID: "probe-1"}, Context: ctx, Options: json.RawMessage(`{}`),
		})))
	}
	step.Effects = append(step.Effects, s.effect(abi.EffectLiveness, abi.F64Payload(ev.Now+1000)))
	step.Notices = append(step.Notices, notice(abi.NoticePublished, payload.Published{Submissions: []payload.SubmissionSettlement{{RequestID: sub.RequestID, Status: "placed"}}}))
	return step
}

func (s *Session) modelEvent(ev abi.Event) *abi.Step {
	switch {
	case bytes.Contains(ev.Payload, []byte(`"type":"error"`)):
		s.complete(ev.ID)
		return &abi.Step{Commits: []abi.Commit{s.commit(fmt.Sprintf("model-error:%d", ev.ID), ev.Now, ev.Payload, false)},
			Notices: []abi.Notice{notice(abi.NoticeReport, map[string]any{"modelError": ev.ID})}}
	case bytes.Contains(ev.Payload, []byte(`"type":"done"`)):
		s.complete(ev.ID)
		args, _ := json.Marshal(payload.ToolEffect{TaskID: 1, ConversationID: 1, ToolName: "lookup", CallID: fmt.Sprintf("call-%d", ev.ID), Arguments: json.RawMessage(`{"n":1}`), Replay: "safe"})
		step := &abi.Step{Commits: []abi.Commit{s.commit(fmt.Sprintf("model:%d", ev.ID), ev.Now, ev.Payload, false)}}
		step.Effects = append(step.Effects, s.effect(abi.EffectTool, args))
		return step
	}
	return &abi.Step{}
}

func (s *Session) inspect(ev abi.Event) *abi.Step {
	var q payload.InspectQuery
	if err := json.Unmarshal(ev.Payload, &q); err != nil {
		return reject("InvalidInspect", err.Error())
	}
	switch q.Query {
	case "log":
		s.inspectID = q.RequestID
		return &abi.Step{Reads: []abi.Read{{ID: readInspect, SQL: sqlSelectLog}}}
	case "uuid":
		return &abi.Step{Notices: []abi.Notice{{Kind: abi.NoticeAPIResult, Payload: withID(q.RequestID, mustJSON(map[string]string{"uuid": s.uuidv7()}))}}}
	case "memory":
		return &abi.Step{Notices: []abi.Notice{{Kind: abi.NoticeAPIResult, Payload: withID(q.RequestID, mustJSON(s.MemStats()))}}}
	}
	return reject("InvalidInspect", "unknown query "+strings.TrimSpace(q.Query))
}

// MemStats returns the eight counters of the mem_stats export (ABI section 4.1) as the probe knows them.
func (s *Session) MemStats() [8]uint64 {
	return [8]uint64{s.logBytes, uint64(len(s.pending)), uint64(s.nextSeq), 0, 0, s.logBytes, s.logBytes, 0}
}
