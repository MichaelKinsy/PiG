package pico3

import (
	"context"
	"errors"
	"testing"
)

func TestOrdinaryTaskScopeRejectsForeignReadsAndWrites(t *testing.T) {
	var foreignConversation, foreignEntry, foreignInput, foreignTask Id
	results := make(chan map[string]error, 1)
	probe := &Kind{Name: "scope"}
	probe.Initial = func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		child := must(CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) (Id, error) { return tx.CreateConversation(ConversationSpec{}) }))
		grandchild := must(CommitAs(ctx, rt, func(_ context.Context, tx *Tx, _ Task) (Id, error) {
			return tx.CreateConversation(ConversationSpec{Parent: &ConversationParentSpec{ConversationId: child, AtStart: true}})
		}))
		_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			if _, err := tx.Write(child, NewEntry{Kind: "child"}); err != nil {
				return nil, err
			}
			if _, err := tx.Write(grandchild, NewEntry{Kind: "grandchild"}); err != nil {
				return nil, err
			}
			equal(t, *must(tx.Conversation(child)).Owner, rt.TaskId, "child owner")
			equal(t, *must(tx.Conversation(grandchild)).Owner, rt.TaskId, "grandchild owner")
			return nil, nil
		})
		if err != nil {
			return Step{}, err
		}
		if _, err := rt.Context(ctx, grandchild, nil); err != nil {
			return Step{}, err
		}
		if _, err := rt.Rewindable(ctx, child); err != nil {
			return Step{}, err
		}
		if _, err := rt.Sticky(ctx, grandchild); err != nil {
			return Step{}, err
		}
		operations := map[string]func(*Tx) (any, error){
			"conversation": func(tx *Tx) (any, error) { return tx.Conversation(foreignConversation) },
			"entry":        func(tx *Tx) (any, error) { return tx.Entry(foreignEntry) },
			"entries":      func(tx *Tx) (any, error) { return tx.Entries([]Id{foreignEntry}) },
			"input":        func(tx *Tx) (any, error) { return tx.Input(foreignInput) },
			"task":         func(tx *Tx) (any, error) { return tx.Task(foreignTask) },
			"snapshot":     func(tx *Tx) (any, error) { return tx.Snapshot(StickyDoc(foreignConversation)) },
			"config": func(tx *Tx) (any, error) {
				config, err := tx.Config(foreignConversation)
				if err != nil {
					return nil, err
				}
				return config.Get("profile")
			},
			"write": func(tx *Tx) (any, error) { return tx.Write(foreignConversation, NewEntry{Kind: "intrusion"}) },
			"createTask": func(tx *Tx) (any, error) {
				return tx.CreateTask(probe, nil, TaskOptions{ConversationId: &foreignConversation, Background: true})
			},
			"createConversation": func(tx *Tx) (any, error) {
				return tx.CreateConversation(ConversationSpec{Parent: &ConversationParentSpec{ConversationId: foreignConversation, AtStart: true}})
			},
		}
		observed := map[string]error{}
		for name, operation := range operations {
			_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) { return operation(tx) })
			observed[name] = err
		}
		offline := map[string]func() error{
			"context":        func() error { _, err := rt.Context(ctx, foreignConversation, nil); return err },
			"newestEntry":    func() error { _, err := rt.NewestEntry(ctx, foreignConversation, NewestOptions{}); return err },
			"rewindable":     func() error { _, err := rt.Rewindable(ctx, foreignConversation); return err },
			"sticky":         func() error { _, err := rt.Sticky(ctx, foreignConversation); return err },
			"rewindableAsOf": func() error { _, err := rt.RewindableAsOf(ctx, foreignConversation, foreignEntry); return err },
			"sendOwned": func() error {
				_, err := rt.SendOwned(ctx, foreignConversation, SendInput{Content: "intrusion"})
				return err
			},
			"abortConversation": func() error { return rt.AbortConversation(ctx, foreignConversation) },
		}
		for name, operation := range offline {
			observed["runtime."+name] = operation()
		}
		results <- observed
		return done(Completed(nil)), nil
	}
	foreignKind := quickKind(t, "foreign", func(Task) JsonValue { return nil })
	env := openEnv(t, openOptions{taskKinds: []*Kind{probe, foreignKind}})
	foreign := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	foreignConversation = foreign.Id
	foreignInput = must(foreign.Write(bg, NewEntry{Kind: "foreign"}))
	foreignEntry = *must(env.storage.Input(bg, foreignInput)).Entry
	foreignTask = must(HostCommit(bg, foreign, func(_ context.Context, tx *Tx) (TaskRef, error) {
		return tx.CreateTask(foreignKind, nil, TaskOptions{ConversationId: &foreignConversation, Background: true})
	})).Id
	env.untilTerminal(foreignTask)
	ref := createTestTask(t, env, probe, nil)
	equal(t, env.untilTerminal(ref.Id).Outcome.Status, OutcomeCompleted, "probe completed")
	for name, err := range <-results {
		if _, ok := errors.AsType[*Forbidden](err); !ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOrdinaryTaskCannotUseCoreTransactionMethods(t *testing.T) {
	observed := make(chan []error, 1)
	kind := &Kind{Name: "cast-authority", Initial: func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
		var results []error
		for _, operation := range []func(*Tx) error{
			func(tx *Tx) error { _, err := tx.AppendEntry(rt.ConversationId, NewEntry{Kind: "forged"}); return err },
			func(tx *Tx) error { _, err := tx.Send(rt.ConversationId, SendInput{Content: "forged"}); return err },
			func(tx *Tx) error {
				return tx.ResolveInputs([]Id{1}, InputResolution{Status: InputDone, Answer: 1})
			},
			func(tx *Tx) error { return tx.MarkTask(rt.TaskId) },
		} {
			_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) { return nil, operation(tx) })
			results = append(results, err)
		}
		observed <- results
		return done(Completed(nil)), nil
	}}
	env := openEnv(t, openOptions{taskKinds: []*Kind{kind}})
	ref := createTestTask(t, env, kind, nil)
	equal(t, env.untilTerminal(ref.Id).Outcome.Status, OutcomeCompleted, "probe")
	for index, err := range <-observed {
		if _, ok := errors.AsType[*Forbidden](err); !ok {
			t.Errorf("operation %d: %v", index, err)
		}
	}
}
