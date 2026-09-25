package session_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

func legacyRecord(kind, id string, parent *string, fields map[string]any) map[string]any {
	record := map[string]any{"type": kind, "id": id, "parentId": parent, "timestamp": "2023-11-14T22:13:20.000Z"}
	maps.Copy(record, fields)
	return record
}
func legacyMessage(id string, parent *string) map[string]any {
	return legacyRecord("message", id, parent, map[string]any{"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": id}}, "timestamp": now}})
}
func legacyFixture(t *testing.T, options session.JsonlStorageOptions, records ...map[string]any) string {
	t.Helper()
	content := string(mustJSON(t, map[string]any{"type": "session", "version": 3, "id": "legacy", "timestamp": "2023-11-14T22:13:20.000Z", "cwd": "/workspace"})) + "\n"
	for _, record := range records {
		content += string(mustJSON(t, record)) + "\n"
	}
	mustNoErr(t, options.FileSystem.WriteFile(background, options.Path, []byte(content)))
	return content
}

func TestJsonlLegacyUpgradeFailurePreservesStateAndUsage(t *testing.T) {
	options := jsonlOptions(t)
	fs := &failingJsonlFS{FileSystem: options.FileSystem, failure: errors.New("rename failed")}
	options.FileSystem = fs
	record := legacyMessage("assistant", nil)
	record["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "answer"}}, "api": "anthropic-messages", "provider": "anthropic", "model": "test", "stopReason": "stop", "timestamp": now, "usage": map[string]any{"input": 10, "output": 5, "totalTokens": 15, "cost": map[string]any{"total": 0.5}}}
	original := legacyFixture(t, options, record)
	storage, err := session.OpenJsonlStorage(background, options)
	mustNoErr(t, err)
	entries, err := storage.ScanEntries(background, session.EntryScan{Order: "asc"})
	mustNoErr(t, err)
	stats, err := storage.GetStats(background)
	mustNoErr(t, err)
	if stats.Usage.TotalTokens != 15 || len(entries) != 1 || entries[0].ID == "assistant" {
		t.Fatalf("import=%+v %+v", stats, entries)
	}
	empty, err := storage.Commit(background, nil)
	mustNoErr(t, err)
	if got := readFile(t, fs, options.Path); got != original {
		t.Fatal("empty commit rewrote legacy file")
	}
	fs.method = "rename"
	_, err = storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "converted")})
	if !errors.Is(err, fs.failure) {
		t.Fatalf("error=%v", err)
	}
	if got := readFile(t, fs, options.Path); got != original {
		t.Fatal("failed conversion rewrote source")
	}
	after, err := storage.GetStats(background)
	mustNoErr(t, err)
	assertJSON(t, after, stats)
	name, err := session.GetValue(background, storage, session.SessionName)
	mustNoErr(t, err)
	if name != nil {
		t.Fatal("failed conversion changed state")
	}
	fs.method = ""
	committed, err := storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "converted")})
	mustNoErr(t, err)
	if committed.FirstSeq != empty.FirstSeq+1 || len(committed.Seqs) != 1 {
		t.Fatalf("caller sequence=%+v empty=%+v", committed, empty)
	}
	rows, err := storage.ScanUsage(background, session.UsageScan{Order: "asc"})
	mustNoErr(t, err)
	if len(rows) != 1 || !rows[0].Adjustment || rows[0].Usage.TotalTokens != 15 {
		t.Fatalf("usage=%+v", rows)
	}
	mustNoErr(t, storage.Close(background))
	reopened, err := session.OpenJsonlStorage(background, options)
	mustNoErr(t, err)
	defer func() { _ = reopened.Close(background) }()
	restored, err := reopened.ScanEntries(background, session.EntryScan{Order: "asc"})
	mustNoErr(t, err)
	assertJSON(t, restored, entries)
	after, err = reopened.GetStats(background)
	mustNoErr(t, err)
	assertJSON(t, after, stats)
	if !strings.Contains(readFile(t, fs, options.Path), `"v":4`) {
		t.Fatal("not upgraded")
	}
}

func TestJsonlLegacySelectedCompactionsAndCapturedPrefix(t *testing.T) {
	options := jsonlOptions(t)
	legacyFixture(t, options, legacyMessage("root", nil), legacyMessage("left", new("root")), legacyRecord("compaction", "left-compaction", new("left"), map[string]any{"summary": "left", "firstKeptEntryId": "root", "tokensBefore": 10}), legacyMessage("right", new("root")), legacyRecord("compaction", "right-compaction", new("right"), map[string]any{"summary": "right", "firstKeptEntryId": "root", "tokensBefore": 20, "fromHook": true}))
	source, err := session.ReadLegacyV3Source(background, options.FileSystem, options.Path)
	mustNoErr(t, err)
	structures, err := source.EntryStructures()
	mustNoErr(t, err)
	selected := map[string]bool{structures[2].ID: true, structures[4].ID: true}
	mustNoErr(t, options.FileSystem.AppendFile(background, options.Path, []byte(string(mustJSON(t, legacyMessage("later", new("right-compaction"))))+"\n")))
	var entries []session.Entry
	mustNoErr(t, source.Writes(background, func(id string) bool { return selected[id] }, func(write session.CommittedWrite) error {
		if entry, ok := write.(session.CommittedEntryWrite); ok {
			entries = append(entries, entry.Entry)
		}
		return nil
	}))
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	assertJSON(t, entries[0].RetainedTail, []any{legacyMessage("root", nil)["message"], legacyMessage("left", nil)["message"]})
	assertJSON(t, entries[1].RetainedTail, []any{legacyMessage("root", nil)["message"], legacyMessage("right", nil)["message"]})
	if !entries[1].FromHook || entries[0].Seq != 3 || entries[1].Seq != 5 {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestJsonlLegacyConfigurationLabelsAndDiscardedParents(t *testing.T) {
	options := jsonlOptions(t)
	legacyFixture(t, options, legacyMessage("root", nil), legacyRecord("model_change", "model", new("root"), map[string]any{"provider": "anthropic", "modelId": "selected"}), legacyRecord("thinking_level_change", "thinking", new("model"), map[string]any{"thinkingLevel": "high"}), legacyRecord("model_change", "other", new("root"), map[string]any{"provider": "openai", "modelId": "abandoned"}), legacyRecord("session_info", "name", new("thinking"), map[string]any{"name": "imported"}), legacyRecord("label", "label", new("name"), map[string]any{"targetId": "name", "label": "important"}), legacyMessage("tip", new("label")))
	storage, err := session.OpenJsonlStorage(background, options)
	mustNoErr(t, err)
	defer func() { _ = storage.Close(background) }()
	entries, err := storage.ScanEntries(background, session.EntryScan{Order: "asc"})
	mustNoErr(t, err)
	if len(entries) != 2 || entries[1].ParentID == nil || *entries[1].ParentID != entries[0].ID {
		t.Fatalf("entries=%+v", entries)
	}
	config, err := session.GetValue(background, storage, session.LaneConfig("main"))
	mustNoErr(t, err)
	if config == nil || config.Value.Model.ModelID != "selected" || config.Value.ThinkingLevel != "high" {
		t.Fatalf("config=%+v", config)
	}
	label, err := session.GetValue(background, storage, session.EntryLabel(entries[0].ID))
	mustNoErr(t, err)
	if label == nil || label.Value != "important" {
		t.Fatalf("label=%+v", label)
	}
	tip, err := session.GetValue(background, storage, session.BranchTip("main"))
	mustNoErr(t, err)
	if tip == nil || tip.Value == nil || *tip.Value != entries[1].ID {
		t.Fatalf("tip=%+v", tip)
	}
}

func TestJsonlLegacyForkClosedAndUpgradeOpen(t *testing.T) {
	options := jsonlOptions(t)
	original := legacyFixture(t, options, legacyRecord("model_change", "model", nil, map[string]any{"provider": "anthropic", "modelId": "test"}), legacyRecord("thinking_level_change", "thinking", new("model"), map[string]any{"thinkingLevel": "high"}), legacyMessage("first", new("thinking")), legacyMessage("last", new("first")))
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: options.FileSystem, SessionsRoot: "sessions", Now: fixedNow})
	defer func() { _ = repo.Close(background) }()
	metadata := session.SessionMetadata{ID: "legacy", Cwd: "/workspace", Path: options.Path, StorageVersion: session.JSONLStorageVersion, CreatedAt: now}
	fork, err := repo.Fork(background, metadata, session.ForkOptions{ID: "closed-fork", Scope: "branch", Branch: "main", EntryID: new("first")})
	mustNoErr(t, err)
	entries, err := fork.FindEntries(background, &session.EntryQuery{Order: "asc"})
	mustNoErr(t, err)
	if len(entries) != 1 || entries[0].ID == "first" {
		t.Fatalf("fork=%+v", entries)
	}
	stats, err := fork.GetStats(background)
	mustNoErr(t, err)
	if stats.Usage.TotalTokens != 0 {
		t.Fatal("fork copied usage")
	}
	mustNoErr(t, fork.Close(background))
	if readFile(t, options.FileSystem, options.Path) != original {
		t.Fatal("closed fork rewrote source")
	}
	source, err := repo.Open(background, metadata)
	mustNoErr(t, err)
	_, err = repo.Fork(background, metadata, session.ForkOptions{ID: "open-fork", Scope: "tree"})
	wantErrContains(t, err, "Cannot fork an open legacy v3 JSONL session")
	before, err := source.FindEntries(background, &session.EntryQuery{Order: "asc"})
	mustNoErr(t, err)
	mustNoErr(t, source.SetName(background, new("upgraded")))
	fork, err = repo.Fork(background, metadata, session.ForkOptions{ID: "open-fork", Scope: "tree"})
	mustNoErr(t, err)
	after, err := fork.FindEntries(background, &session.EntryQuery{Order: "asc"})
	mustNoErr(t, err)
	assertJSON(t, after, before)
	mustNoErr(t, fork.Close(background))
	mustNoErr(t, source.Close(background))
}

func TestJsonlLegacyInvalidReferences(t *testing.T) {
	for _, kind := range []string{"missing-parent", "duplicate", "null-id", "summary-source", "compaction-boundary"} {
		t.Run(kind, func(t *testing.T) {
			options := jsonlOptions(t)
			records := []map[string]any{legacyMessage("root", nil)}
			switch kind {
			case "missing-parent":
				records = append(records, legacyMessage("child", new("missing")))
			case "duplicate":
				records = append(records, legacyMessage("root", nil))
			case "null-id":
				records[0]["id"] = nil
			case "summary-source":
				records = append(records, legacyRecord("branch_summary", "summary", new("root"), map[string]any{"fromId": "missing", "summary": "summary"}))
			case "compaction-boundary":
				records = append(records, legacyRecord("compaction", "compaction", new("root"), map[string]any{"firstKeptEntryId": "missing", "summary": "summary", "tokensBefore": 1}))
			}
			original := legacyFixture(t, options, records...)
			_, err := session.OpenJsonlStorage(background, options)
			if err == nil {
				t.Fatal("invalid legacy source accepted")
			}
			if readFile(t, options.FileSystem, options.Path) != original {
				t.Fatal("invalid source rewritten")
			}
		})
	}
}

func TestJsonlLegacyTailProjectsCustomAndSummaryNodes(t *testing.T) {
	options := jsonlOptions(t)
	legacyFixture(t, options, legacyMessage("ordinary", nil), legacyRecord("custom_message", "notice", new("ordinary"), map[string]any{"customType": "notice", "content": "hello", "display": false, "details": map[string]any{"ok": true}}), legacyRecord("custom", "opaque", new("notice"), map[string]any{"customType": "checkpoint", "data": map[string]any{"reference": "ordinary"}}), legacyRecord("branch_summary", "summary", new("opaque"), map[string]any{"fromId": "ordinary", "summary": "earlier"}), legacyRecord("compaction", "older", new("summary"), map[string]any{"summary": "old", "firstKeptEntryId": "ordinary", "tokensBefore": 10}), legacyRecord("compaction", "final", new("older"), map[string]any{"summary": "final", "firstKeptEntryId": "ordinary", "tokensBefore": 20}))
	storage, err := session.OpenJsonlStorage(background, options)
	mustNoErr(t, err)
	defer func() { _ = storage.Close(background) }()
	entries, err := storage.ScanEntries(background, session.EntryScan{Order: "asc"})
	mustNoErr(t, err)
	if len(entries) != 6 {
		t.Fatalf("entries=%+v", entries)
	}
	tail := entries[5].RetainedTail
	if len(tail) != 4 {
		t.Fatalf("tail=%+v", tail)
	}
	assertJSON(t, tail[0], legacyMessage("ordinary", nil)["message"])
	assertJSON(t, tail[1], map[string]any{"role": "custom", "customType": "notice", "content": "hello", "display": false, "details": map[string]any{"ok": true}, "timestamp": now})
	assertJSON(t, tail[2], map[string]any{"role": "branchSummary", "summary": "earlier", "fromId": entries[0].ID, "timestamp": now})
	assertJSON(t, tail[3], map[string]any{"role": "compactionSummary", "summary": "old", "tokensBefore": 10, "timestamp": now})
	assertJSON(t, *entries[2].Data, map[string]any{"reference": "ordinary"})
}

func TestJsonlLegacyEmptyAndClearedMetadata(t *testing.T) {
	for _, cleared := range []any{nil, ""} {
		options := jsonlOptions(t)
		nameFields := map[string]any{}
		labelFields := map[string]any{"targetId": "root"}
		if cleared != nil {
			nameFields["name"] = cleared
			labelFields["label"] = cleared
		}
		legacyFixture(t, options, legacyMessage("root", nil), legacyRecord("session_info", "name", new("root"), map[string]any{"name": "old"}), legacyRecord("label", "label", new("name"), map[string]any{"targetId": "root", "label": "old"}), legacyRecord("session_info", "clear-name", new("label"), nameFields), legacyRecord("label", "clear-label", new("clear-name"), labelFields))
		storage, err := session.OpenJsonlStorage(background, options)
		mustNoErr(t, err)
		entries, err := storage.ScanEntries(background, session.EntryScan{})
		mustNoErr(t, err)
		name, err := session.GetValue(background, storage, session.SessionName)
		mustNoErr(t, err)
		label, err := session.GetValue(background, storage, session.EntryLabel(entries[0].ID))
		mustNoErr(t, err)
		config, err := session.GetValue(background, storage, session.LaneConfig("main"))
		mustNoErr(t, err)
		if name != nil || label != nil || config != nil {
			t.Fatalf("metadata not cleared: %+v %+v %+v", name, label, config)
		}
		mustNoErr(t, storage.Close(background))
	}
	options := jsonlOptions(t)
	legacyFixture(t, options)
	storage, err := session.OpenJsonlStorage(background, options)
	mustNoErr(t, err)
	defer func() { _ = storage.Close(background) }()
	tip, err := session.GetValue(background, storage, session.BranchTip("main"))
	mustNoErr(t, err)
	if tip == nil || tip.Value != nil {
		t.Fatalf("empty main=%+v", tip)
	}
	_, err = storage.Commit(background, []session.Write{session.SetValue(session.SessionName, "converted")})
	mustNoErr(t, err)
	rows, err := storage.ScanUsage(background, session.UsageScan{})
	mustNoErr(t, err)
	if len(rows) != 1 || !rows[0].Adjustment || rows[0].Usage.TotalTokens != 0 {
		t.Fatalf("zero adjustment=%+v", rows)
	}
}
