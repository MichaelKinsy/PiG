package pico3

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReservedAndDuplicateKindRegistrationReject(t *testing.T) {
	if _, err := DefineTask(Kind{Name: "pi.generation"}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved definition: %v", err)
	}
	duplicate := quickKind(t, "dup", func(Task) JsonValue { return nil })
	copyKind := *duplicate
	for _, kinds := range [][]*Kind{{{Name: "pi.generation"}}, {duplicate, &copyKind}} {
		storage := NewMemoryStorage()
		h, err := OpenHarness(bg, storage, HarnessOptions{Models: newFake(fakeOptions{respond: echoScript}), TaskKinds: kinds})
		if err == nil {
			check(t, h.Close(bg))
			t.Fatal("invalid registry accepted")
		}
		check(t, storage.Close(bg))
	}
}

func TestRegistryStaleHooksAndUnsubscribeIdentity(t *testing.T) {
	kind := quickKind(t, "registered", func(Task) JsonValue { return nil })
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}, tools: []*ToolDeclaration{newTool("x", toolOptions{}).ToolDeclaration}})
	namespace := must(env.h.Namespace("test.hooks", NamespaceDefaults{}, nil))
	stale := *kind
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		return tx.CreateTask(&stale, nil, TaskOptions{ConversationId: new(Id(1))})
	})
	if _, ok := errors.AsType[*Forbidden](err); !ok {
		t.Fatalf("stale kind %v", err)
	}
	if _, err := env.h.Hooks(namespace, &stale, &ToolHooks{}); err == nil {
		t.Fatal("stale global hook accepted")
	}
	toolCopy := *Kinds.Tool
	if _, err := env.root.Hooks(namespace, &toolCopy, &ToolHooks{}, false); err == nil {
		t.Fatal("stale local hook accepted")
	}
	var seen []string
	register := func(label string) func() {
		return must(env.h.Hooks(namespace, Kinds.Tool, &ToolHooks{BeforeTool: func(_ context.Context, call JsonObject, _ *BeforeToolApi) (*BeforeToolResult, error) {
			seen = append(seen, label+":"+str(call, "name"))
			return nil, nil
		}}))
	}
	off := register("old")
	off()
	current := register("new")
	off()
	env.wait(env.send(env.root, "tool:x"))
	equal(t, seen, []string{"new:x"}, "current hook retained")
	current()
	current()
}

func TestToolSectionEntryRegistryIdentity(t *testing.T) {
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{newTool("x", toolOptions{}).ToolDeclaration}})
	if _, err := env.h.RegisterTool(newTool("x", toolOptions{}).ToolDeclaration); err == nil {
		t.Fatal("duplicate tool accepted")
	}
	off := must(env.h.RegisterTool(newTool("y", toolOptions{}).ToolDeclaration))
	off()
	must(env.h.RegisterTool(newTool("y", toolOptions{}).ToolDeclaration))
	off()
	if _, err := env.h.RegisterTool(newTool("y", toolOptions{}).ToolDeclaration); err == nil {
		t.Fatal("old unsubscribe removed new tool")
	}
	section := DefineSystemSection("s", func(value JsonValue) string { return value.(string) })
	offSection := must(env.h.RegisterSection(section))
	if _, err := env.h.RegisterSection(DefineSystemSection("s", section.Render)); err == nil {
		t.Fatal("duplicate section accepted")
	}
	offSection()
	offSection()
	must(env.h.RegisterEntryKind(must(DefineEntry("e"))))
	if _, err := env.h.RegisterEntryKind(must(DefineEntry("e"))); err == nil {
		t.Fatal("duplicate entry accepted")
	}
	if _, err := env.h.RegisterEntryKind(&EntryKind{Kind: "pi.mine"}); err == nil {
		t.Fatal("reserved entry accepted")
	}
}

func TestStorageHasOneSessionOwnerAndReleasesOnClose(t *testing.T) {
	storage := NewMemoryStorage()
	options := HarnessOptions{Models: newFake(fakeOptions{respond: echoScript})}
	h := must(OpenHarness(bg, storage, options))
	if second, err := OpenHarness(bg, storage, options); err == nil {
		check(t, second.Close(bg))
		t.Fatal("duplicate storage owner accepted")
	}
	check(t, h.Close(bg))
	again := must(OpenHarness(bg, storage, options))
	check(t, again.Close(bg))
}

func TestHostTransactionSurfaceExpiresAndForbidsCoreMethods(t *testing.T) {
	env := openEnv(t, openOptions{})
	state := must(env.h.Namespace("test.host", NamespaceDefaults{Session: JsonObject{"s": 0}}, nil))
	var escaped *Tx
	var config *ConfigAccess
	_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		escaped = tx
		config = must(tx.Config(1))
		return nil, nil
	})
	check(t, err)
	if _, err := escaped.Config(1); err == nil || !strings.Contains(err.Error(), "outside its callback") {
		t.Fatalf("escaped tx: %v", err)
	}
	if _, err := config.Get("profile"); err == nil || !strings.Contains(err.Error(), "outside its callback") {
		t.Fatalf("escaped config: %v", err)
	}
	for _, operation := range []func(*Tx) error{
		func(tx *Tx) error { _, err := tx.Send(1, SendInput{Content: "x"}); return err },
		func(tx *Tx) error { _, err := tx.AppendEntry(1, NewEntry{Kind: "x"}); return err },
		func(tx *Tx) error { _, err := tx.Sticky(1); return err },
		func(tx *Tx) error { _, err := tx.Boundary(1, "final", nil); return err },
		func(tx *Tx) error {
			_, err := tx.CreateTask(Kinds.Generation, JsonObject{"inputs": []any{}}, TaskOptions{ConversationId: new(Id(1))})
			return err
		},
		func(tx *Tx) error { return tx.Checkpoint(Checkpoint{"phase": "x"}) },
		func(tx *Tx) error { _, err := tx.Slot(TaskRef{Id: 1, Kind: Kinds.Job}); return err },
	} {
		_, err := env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) { return nil, operation(tx) })
		if _, ok := errors.AsType[*Forbidden](err); !ok {
			t.Fatalf("core operation: %v", err)
		}
	}
	_, err = env.root.Commit(bg, func(_ context.Context, tx *Tx) (any, error) {
		must(tx.Plugins(state)).Set("s", 1)
		return nil, must(tx.Config(1)).Set("profile", "p2")
	})
	check(t, err)
	equal(t, must(env.root.Config().Get(bg))["profile"], "p2", "host config")
}

func TestOrdinaryRuntimeCannotAbortForeignTask(t *testing.T) {
	var target Id
	observed := make(chan error, 1)
	attacker := &Kind{Name: "attacker", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		_, err := rt.AbortTask(ctx, target)
		observed <- err
		return done(Completed(nil)), nil
	}}
	gate := &testGate{}
	env := openEnv(t, openOptions{taskKinds: []*Kind{attacker}, models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	active := env.send(env.root, "root")
	gate.Arrivals(t, 1)
	target = firstOfKind(t, env.tasks(), "pi.generation").Id
	foreign := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	ref := must(HostCommit(bg, foreign, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(attacker, nil, TaskOptions{ConversationId: &foreign.Id, Background: true})
	}))
	env.untilTerminal(ref.Id)
	if _, ok := errors.AsType[*Forbidden](<-observed); !ok {
		t.Fatal("foreign abort accepted")
	}
	equal(t, must(env.h.GetTask(bg, target)).Abort, false, "target unmarked")
	gate.Open()
	equal(t, env.wait(active).Status, InputDone, "active completed")
}

func TestCapturedRuntimeRejectsDurableMarkAndTerminalOperations(t *testing.T) {
	captured := make(chan *Runtime, 1)
	gate := &testGate{}
	kind := &Kind{Name: "marked", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		captured <- rt
		return done(Completed(nil)), gate.Wait(ctx)
	}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, nil)
	gate.Arrivals(t, 1)
	rt := <-captured
	_, err := env.h.MarkTask(bg, ref.Id)
	check(t, err)
	assertForbidden := func(err error) {
		t.Helper()
		if _, ok := errors.AsType[*Forbidden](err); !ok {
			t.Fatalf("expired capability: %v", err)
		}
	}
	_, err = rt.Commit(bg, func(context.Context, *Tx, Task) (any, error) { return 1, nil })
	assertForbidden(err)
	gate.Open()
	env.untilTerminal(ref.Id)
	_, err = rt.Commit(bg, func(context.Context, *Tx, Task) (any, error) { return 1, nil })
	assertForbidden(err)
	var child Id
	normal := &Kind{Name: "normal", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		captured <- rt
		var err error
		child, err = rt.CreateOwnedConversation(ctx, OwnedConversationSpec{})
		return done(Completed(nil)), err
	}}
	must(env.h.RegisterTaskKind(normal))
	ref = createTestTask(t, env, normal, nil)
	env.untilTerminal(ref.Id)
	rt = <-captured
	_, err = rt.Commit(bg, func(context.Context, *Tx, Task) (any, error) { return 1, nil })
	assertForbidden(err)
	_, err = rt.CreateOwnedConversation(bg, OwnedConversationSpec{})
	assertForbidden(err)
	_, err = rt.SendOwned(bg, child, SendInput{Content: "late"})
	assertForbidden(err)
}

func TestEveryCoreCapabilityRejectsOrdinaryTask(t *testing.T) {
	gate := &testGate{}
	other := &Kind{Name: "other", Initial: func(ctx context.Context, _ Task, _ *Runtime) (Step, error) {
		return done(Completed(nil)), gate.Wait(ctx)
	}}
	var otherID, foreign Id
	var namespace, stale *Namespace
	observed := make(chan map[string]error, 1)
	probe := &Kind{Name: "all-capabilities", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		operations := map[string]func(*Tx) error{
			"send":        func(tx *Tx) error { _, err := tx.Send(1, SendInput{Content: "x"}); return err },
			"boundary":    func(tx *Tx) error { _, err := tx.Boundary(1, "final", nil); return err },
			"resolve":     func(tx *Tx) error { return tx.ResolveInputs([]Id{1}, InputResolution{Status: InputDone, Answer: 1}) },
			"rewindable":  func(tx *Tx) error { _, err := tx.Rewindable(1); return err },
			"sticky":      func(tx *Tx) error { _, err := tx.Sticky(1); return err },
			"session":     func(tx *Tx) error { _, err := tx.Session(); return err },
			"toolSlot":    func(tx *Tx) error { _, err := tx.ToolSlot(1, 0); return err },
			"appendEntry": func(tx *Tx) error { _, err := tx.AppendEntry(1, NewEntry{Kind: "x"}); return err },
			"coreByName": func(tx *Tx) error {
				_, err := tx.CreateTaskSpec(TaskSpec{Kind: "pi.generation", ConversationId: new(Id(1)), Input: JsonObject{"inputs": []any{}}})
				return err
			},
			"coreByToken": func(tx *Tx) error {
				_, err := tx.CreateTask(Kinds.Generation, JsonObject{"inputs": []any{}}, TaskOptions{ConversationId: new(Id(1))})
				return err
			},
			"mark":      func(tx *Tx) error { return tx.MarkTask(1) },
			"writeHead": func(tx *Tx) error { _, err := tx.Write(1, NewEntry{Kind: "x", Head: &HeadRef{Self: true}}); return err },
			"writeReserved": func(tx *Tx) error {
				_, err := tx.Write(1, NewEntry{Kind: "pi.assistant", Model: []JsonObject{}})
				return err
			},
			"otherSlot":    func(tx *Tx) error { _, err := tx.Slot(TaskRef{Id: otherID, Kind: other}); return err },
			"foreignWrite": func(tx *Tx) error { _, err := tx.Write(foreign, NewEntry{Kind: "x"}); return err },
			"foreignRead":  func(tx *Tx) error { _, err := tx.NewestEntry(foreign, NewestOptions{}); return err },
			"foreignParent": func(tx *Tx) error {
				_, err := tx.CreateConversation(ConversationSpec{Parent: &ConversationParentSpec{ConversationId: foreign, AtStart: true}})
				return err
			},
			"foreignConfig":    func(tx *Tx) error { _, err := tx.Config(foreign); return err },
			"coreConfigWrite":  func(tx *Tx) error { return must(tx.Config(1)).Set("profile", "forged") },
			"stalePlugin":      func(tx *Tx) error { _, err := tx.Plugins(stale); return err },
			"ownWriteAllowed":  func(tx *Tx) error { _, err := tx.Write(1, NewEntry{Kind: "x"}); return err },
			"ownPluginAllowed": func(tx *Tx) error { must(tx.Plugins(namespace)).Set("ok", true); return nil },
		}
		result := map[string]error{}
		for name, operation := range operations {
			_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) { return nil, operation(tx) })
			result[name] = err
		}
		observed <- result
		return done(Completed(nil)), nil
	}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{other, probe}})
	stale = must(env.h.Namespace("task.plugin", NamespaceDefaults{Sticky: JsonObject{"ok": false}}, nil))
	stale.Unregister()
	namespace = must(env.h.Namespace("task.plugin", NamespaceDefaults{Sticky: JsonObject{"ok": false}}, nil))
	foreign = must(env.h.CreateConversation(bg, ConversationSpec{}, nil)).Id
	otherID = createTestTask(t, env, other, nil).Id
	gate.Arrivals(t, 1)
	ref := createTestTask(t, env, probe, nil)
	equal(t, env.untilTerminal(ref.Id).Outcome.Status, OutcomeCompleted, "probe")
	gate.Open()
	for name, err := range <-observed {
		if strings.HasSuffix(name, "Allowed") {
			check(t, err)
		} else if _, ok := errors.AsType[*Forbidden](err); !ok {
			t.Errorf("%s returned %v", name, err)
		}
	}
}
