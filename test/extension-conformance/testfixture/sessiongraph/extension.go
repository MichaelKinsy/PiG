// Package sessiongraph is the Go SDK extension of the session graph conformance rows (session_graph_test.go): Pi's SessionManager constructors, listing and log methods as an extension builds them. The Node (testdata/session-graph.mjs), Rust (testdata/session-graph-rust) and Python (the source in session_graph_test.go) fixtures run the same script, so one table of rows proves each SDK; the Node one runs Pi's own classes. Every value the script reports is one the host or Pi produces: entry ids are random, so the report names an entry by the index of its parent.
package sessiongraph

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Name is the extension's registered name.
const Name = "session-graph"

// ok is the value of a call that must not fail; a failure ends the script with its error.
func ok[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func orNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// shape reports each entry as its type and the index of its parent in entries (-1 for a root), so the random entry ids do not show.
func shape(entries []map[string]any) []string {
	index := map[string]int{}
	for i, entry := range entries {
		index[fmt.Sprint(entry["id"])] = i
	}
	out := []string{}
	for _, entry := range entries {
		parent := -1
		if id, isString := entry["parentId"].(string); isString {
			if at, found := index[id]; found {
				parent = at
			}
		}
		out = append(out, fmt.Sprintf("%v<%d", entry["type"], parent))
	}
	return out
}

func failure(err error, contains string) string {
	switch {
	case err == nil:
		return "no error"
	case strings.Contains(err.Error(), contains):
		return "error: " + contains
	}
	return "error: " + err.Error()
}

func infos(list []sdk.SessionInfo) []map[string]any {
	out := []map[string]any{}
	for _, info := range list {
		out = append(out, map[string]any{
			"id": info.ID, "messageCount": info.MessageCount, "name": orNil(info.Name), "firstMessage": info.FirstMessage,
			"cwd": info.Cwd, "createdRecent": info.Created.UnixMilli() > 1767225600000, "modifiedMs": info.Modified.UnixMilli(), "fileInDir": info.Path != "",
		})
	}
	slices.SortFunc(out, func(a, b map[string]any) int { return strings.Compare(a["id"].(string), b["id"].(string)) })
	return out
}

var assistant = map[string]any{
	"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hi"}}, "api": "test", "provider": "p", "model": "m",
	"usage":      map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
	"stopReason": "stop", "timestamp": 2,
}

func script(ctx sdk.Context, args map[string]any) map[string]any {
	cwd, dir, otherDir, junk := fmt.Sprint(args["cwd"]), fmt.Sprint(args["dir"]), fmt.Sprint(args["otherDir"]), fmt.Sprint(args["junk"])
	r := map[string]any{}

	fixed := "fixed-session-1"
	m := ok(sdk.SessionManagerCreate(ctx, cwd, &dir, &sdk.NewSessionOptions{ID: &fixed}))
	file := *ok(m.GetSessionFile())
	r["created"] = map[string]any{
		"id": ok(m.GetSessionID()), "persisted": ok(m.IsPersisted()), "dir": ok(m.GetSessionDir()),
		"fileSuffix": strings.HasSuffix(file, "_fixed-session-1.jsonl"), "fileInDir": filepath.Dir(file) == dir, "cwd": ok(m.GetCwd()),
		"defaultDir": ok(m.UsesDefaultSessionDir()), "entries": ok(m.GetEntryCount()), "leaf": orNil(ok(m.GetLeafID())),
	}
	first := ok(m.AppendMessage(map[string]any{"role": "user", "content": "hello", "timestamp": 1}))
	ok(m.AppendMessage(assistant))
	ok(m.AppendSessionInfo("graph"))
	label := "start"
	ok(m.AppendLabelChange(first, &label))
	ok(m.AppendCustomEntry("note", map[string]any{"a": 1, "b": 2}))
	ok(m.AppendThinkingLevelChange("high"))
	last := ok(m.AppendModelChange("p", "m"))
	entries := ok(m.GetEntries())
	leaf := ok(m.GetLeafID())
	header := ok(m.GetHeader())
	custom := map[string]any{}
	for _, entry := range entries {
		if entry["type"] == "custom" {
			custom = entry
		}
	}
	r["log"] = map[string]any{
		"name": orNil(ok(m.GetSessionName())), "label": orNil(ok(m.GetLabel(first))), "count": ok(m.GetEntryCount()), "shape": shape(entries),
		"leafIsLast": leaf != nil && *leaf == last, "entryType": ok(m.GetEntry(first))["type"], "missing": ok(m.GetEntry("nope")) == nil,
		"headerType": header["type"], "headerVersion": header["version"], "headerId": header["id"], "headerCwd": header["cwd"],
		"customType": custom["customType"], "customData": custom["data"],
	}
	built := ok(m.BuildSessionContext())
	r["context"] = map[string]any{"messages": len(built.Messages), "thinkingLevel": built.ThinkingLevel, "model": built.Model}
	tree := ok(m.GetTree())
	r["tree"] = map[string]any{"roots": len(tree), "firstChildren": len(tree[0].Children)}

	msgID := ok(m.AppendCustomMessageEntry("note-msg", "body", true, map[string]any{"k": 1}))
	usage := ok(m.AppendUsage("test", "p", "m", map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 3, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, nil))
	r["extra"] = map[string]any{"msgType": ok(m.GetEntry(msgID))["type"], "msgCustomType": ok(m.GetEntry(msgID))["customType"], "usageType": usage["type"], "usageKind": usage["kind"]}

	ok(0, m.Branch(first))
	alt := ok(m.AppendMessage(map[string]any{"role": "user", "content": "alt", "timestamp": 3}))
	branch := ok(m.GetBranch(nil))
	children := ok(m.GetChildren(first))
	r["branch"] = map[string]any{"shape": shape(branch), "children": len(children), "leafIsAlt": *ok(m.GetLeafID()) == alt}
	ok(0, m.ResetLeaf())
	r["reset"] = map[string]any{"leaf": orNil(ok(m.GetLeafID()))}
	summary := ok(m.BranchWithSummary(&first, "summary text", nil, nil, nil))
	summaryEntry := ok(m.GetEntry(summary))
	r["summary"] = map[string]any{"type": summaryEntry["type"], "parentIsFirst": summaryEntry["parentId"] == first, "text": summaryEntry["summary"], "fromId": summaryEntry["fromId"] == first}
	branched := ok(m.CreateBranchedSession(summary))
	r["branched"] = map[string]any{
		"isFile": branched != nil && strings.HasSuffix(*branched, ".jsonl"), "inDir": branched != nil && filepath.Dir(*branched) == dir,
		"newFile": branched != nil && *branched != file, "idChanged": ok(m.GetSessionID()) != fixed, "count": ok(m.GetEntryCount()), "shape": shape(ok(m.GetEntries())),
	}

	opened := ok(sdk.SessionManagerOpen(ctx, file, nil, nil))
	openedHeader := ok(opened.GetHeader())
	r["opened"] = map[string]any{
		"id": ok(opened.GetSessionID()), "count": ok(opened.GetEntryCount()), "cwd": ok(opened.GetCwd()), "headerCwd": openedHeader["cwd"], "dirIsFileDir": ok(opened.GetSessionDir()) == dir,
		"file": *ok(opened.GetSessionFile()) == file, "name": orNil(ok(opened.GetSessionName())),
	}
	overridden := ok(sdk.SessionManagerOpen(ctx, file, nil, &otherDir))
	r["override"] = map[string]any{"cwd": ok(overridden.GetCwd()), "id": ok(overridden.GetSessionID())}
	recent := ok(sdk.SessionManagerContinueRecent(ctx, cwd, &dir))
	recentFile := ok(recent.GetSessionFile())
	r["recent"] = map[string]any{"inDir": recentFile != nil && filepath.Dir(*recentFile) == dir, "hasEntries": ok(recent.GetEntryCount()) > 0, "cwd": ok(recent.GetCwd())}

	memID := "mem-1"
	mem := ok(sdk.SessionManagerInMemory(ctx, cwd, &sdk.NewSessionOptions{ID: &memID}, nil))
	before := orNil(ok(mem.GetLeafID()))
	ok(mem.AppendMessage(map[string]any{"role": "user", "content": "x", "timestamp": 1}))
	r["memory"] = map[string]any{
		"id": ok(mem.GetSessionID()), "persisted": ok(mem.IsPersisted()), "file": orNil(ok(mem.GetSessionFile())), "dir": ok(mem.GetSessionDir()),
		"defaultDir": ok(mem.UsesDefaultSessionDir()), "leafBefore": before, "count": ok(mem.GetEntryCount()), "cwd": ok(mem.GetCwd()),
	}
	seeded := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, []map[string]any{{"type": "message", "id": "e1", "parentId": nil, "timestamp": "2026-01-01T00:00:00.000Z", "message": map[string]any{"role": "user", "content": "seed", "timestamp": 1}}}))
	r["seeded"] = map[string]any{"count": ok(seeded.GetEntryCount()), "leaf": orNil(ok(seeded.GetLeafID())), "hasId": ok(seeded.GetSessionID()) != ""}

	forkID := "forked-1"
	forkDir := filepath.Join(otherDir, "sessions")
	forked := ok(sdk.SessionManagerForkFrom(ctx, file, otherDir, &forkDir, &sdk.NewSessionOptions{ID: &forkID}))
	forkHeader := ok(forked.GetHeader())
	forkFile := *ok(forked.GetSessionFile())
	r["fork"] = map[string]any{
		"id": ok(forked.GetSessionID()), "count": ok(forked.GetEntryCount()), "parent": forkHeader["parentSession"] == file, "cwd": ok(forked.GetCwd()) == otherDir,
		"inDir": filepath.Dir(forkFile) == forkDir, "shape": shape(ok(forked.GetEntries())),
	}

	r["list"] = infos(ok(sdk.SessionManagerList(ctx, cwd, &dir, nil)))
	var listProgress, listAllProgress [][2]int
	r["listAll"] = infos(ok(sdk.SessionManagerListAll(ctx, &dir, func(loaded, total int, _ []sdk.SessionInfo) {
		listAllProgress = append(listAllProgress, [2]int{loaded, total})
	})))
	infos(ok(sdk.SessionManagerList(ctx, cwd, &dir, func(loaded, total int, _ []sdk.SessionInfo) {
		listProgress = append(listProgress, [2]int{loaded, total})
	})))
	lastProgress := func(calls [][2]int) map[string]any {
		var last any
		if len(calls) > 0 {
			last = []int{calls[len(calls)-1][0], calls[len(calls)-1][1]}
		}
		return map[string]any{"calls": len(calls) > 0, "last": last}
	}
	r["progress"] = map[string]any{"list": lastProgress(listProgress), "listAll": lastProgress(listAllProgress)}
	r["listFork"] = infos(ok(sdk.SessionManagerList(ctx, otherDir, &forkDir, nil)))
	found := ok(sdk.SessionManagerFindByID(ctx, cwd, fixed, &dir))
	missing := ok(sdk.SessionManagerFindByID(ctx, cwd, "nope", &dir))
	r["find"] = map[string]any{"found": found != nil && *found == file, "missing": missing == nil}

	compactionID := ok(m.AppendCompaction("sum", first, 100, map[string]any{"k": 1}, nil, nil))
	compaction := ok(m.GetEntry(compactionID))
	editID := ok(m.AppendContextEdit(first, map[string]any{"content": "edited"}))
	edit := ok(m.GetEntry(editID))
	clearID := ok(m.AppendContextEdit(first, nil))
	reopened := ok(sdk.SessionManagerCreate(ctx, cwd, &dir, &sdk.NewSessionOptions{ID: new("reopen-1")}))
	check(reopened.SetSessionFile(file))
	reopenedFile := *ok(reopened.GetSessionFile())
	_, badTarget := m.AppendContextEdit("nope", nil)
	_, badReplacement := m.AppendContextEdit(first, map[string]any{"content": 5})
	finalLeaf := ok(m.GetLeafID())
	r["compaction"] = map[string]any{
		"type": compaction["type"], "summary": compaction["summary"], "firstKept": compaction["firstKeptEntryId"] == first, "tokensBefore": compaction["tokensBefore"], "details": compaction["details"], "fromHook": compaction["fromHook"],
		"editType": edit["type"], "replacement": edit["replacement"], "target": edit["targetId"] == first, "cleared": ok(m.GetEntry(clearID))["replacement"], "leaf": finalLeaf != nil && *finalLeaf == clearID,
		"badTarget": failure(badTarget, "not found"), "badReplacement": failure(badReplacement, "must be null or contain"),
		"switched": map[string]any{
			"id": ok(reopened.GetSessionID()) == ok(m.GetSessionID()), "file": reopenedFile == file, "shape": strings.Join(shape(ok(reopened.GetEntries())), ",") == strings.Join(shape(ok(m.GetEntries())), ","), "count": ok(reopened.GetEntryCount()),
		},
		"badSwitch": failure(reopened.SetSessionFile(junk), "not a valid"),
	}

	bad := "bad id!"
	_, badID := sdk.SessionManagerCreate(ctx, cwd, &dir, &sdk.NewSessionOptions{ID: &bad})
	_, notSession := sdk.SessionManagerOpen(ctx, junk, nil, nil)
	_, noFork := sdk.SessionManagerForkFrom(ctx, filepath.Join(dir, "missing.jsonl"), cwd, &forkDir, nil)
	r["errors"] = map[string]any{"badId": failure(badID, "Session id must be"), "notSession": failure(notSession, "not a valid"), "noFork": failure(noFork, "Cannot fork")}
	return r
}

// Extension is the fixture.
func Extension() *sdk.Extension {
	e := sdk.New(Name)
	e.RegisterTool(sdk.ToolDefinition{
		Name: "session_manager", Label: "session_manager", Description: "Runs the SessionManager script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(script(ctx, args))
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "settings_manager", Label: "settings_manager", Description: "Runs the SettingsManager script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(settingsScript(ctx, args))
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "project_trust_store", Label: "project_trust_store", Description: "Runs the ProjectTrustStore script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(trustScript(ctx, args))
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "resource_loader", Label: "resource_loader", Description: "Runs the DefaultResourceLoader script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(loaderScript(ctx, args))
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "model_runtime", Label: "model_runtime", Description: "Runs the ModelRuntime script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(modelRuntimeScript(ctx, args))
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "package_manager", Label: "package_manager", Description: "Runs the DefaultPackageManager script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(packagesScript(ctx, args))
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_handles", Label: "child_handles", Description: "Runs the createAgentSession handle options script.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (result any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("%v", recovered)
				}
			}()
			encoded, err := json.Marshal(childHandlesScript(ctx, args))
			return string(encoded), err
		},
	})
	return e
}
