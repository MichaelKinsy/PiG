// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

type fakeEnv struct {
	env.ExecutionEnv
	name string
}

type sectionBackend struct {
	durable.DocumentReader
	agent durable.Agent
}

func (b sectionBackend) Agent(context.Context) (durable.Agent, error) { return b.agent, nil }

// sectionRig opens a core that asks for an env effect and then a section effect, and returns the completions.
func sectionRig(t *testing.T, build func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error), sections []*durable.PromptSection, section int) (*funcCore, *sqlhost.Host, *driver.Envs) {
	t.Helper()
	envs := &driver.Envs{Build: build, Reader: func() durable.DocumentReader { return nil }}
	sec := &driver.Sections{
		Envs: envs,
		Backend: func(durable.TaskId, durable.ConversationId) driver.SectionBackend {
			return sectionBackend{agent: durable.Agent{Sections: sections}}
		},
	}
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		switch {
		case ev.Kind == abi.EventOpen:
			return &abi.Step{Effects: []abi.Effect{{ID: 1, Kind: abi.EffectEnv, Payload: []byte(`{"conversationId":3,"taskId":7,"cwd":"/work"}`)}}}
		case ev.Kind == abi.EventHookDone && ev.ID == 1 && ev.Phase == abi.OutcomeResult:
			payload, _ := jsonSection(section)
			return &abi.Step{Effects: []abi.Effect{{ID: 2, Kind: abi.EffectSection, Payload: payload}}}
		}
		return nil
	}}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	host := sqlhost.New(sqlhost.Options{
		Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectEnv: envs.Handler(), abi.EffectSection: sec.Handler()},
	})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	return core, host, envs
}

func jsonSection(i int) ([]byte, error) {
	return []byte(`{"conversationId":3,"taskId":7,"section":` + string(rune('0'+i)) + `,"input":{"shown":{"a":"1"}}}`), nil
}

func completion(t *testing.T, core *funcCore, host *sqlhost.Host, id uint32) abi.Event {
	t.Helper()
	var got abi.Event
	waitFor(t, "completion", func() bool {
		if host.Err() != nil {
			t.Fatalf("host discarded: %v", host.Err())
		}
		for _, ev := range core.events(abi.EventHookDone) {
			if ev.ID == id {
				got = ev
				return true
			}
		}
		return false
	})
	return got
}

func TestSectionRendersWithTheTasksEnvironmentAndShownSections(t *testing.T) {
	built := &fakeEnv{name: "built"}
	var target harness.EnvTarget
	section := &durable.PromptSection{Key: "k", Render: func(_ context.Context, in durable.PromptInput) (*string, error) {
		if in.ConversationId != 3 || in.Env != env.ExecutionEnv(built) || in.Shown["a"] != "1" {
			t.Errorf("input = %+v", in)
		}
		return new("rendered"), nil
	}}
	core, host, envs := sectionRig(t, func(_ context.Context, tg harness.EnvTarget) (env.ExecutionEnv, error) {
		target = tg
		return built, nil
	}, []*durable.PromptSection{section}, 0)
	if ev := completion(t, core, host, 2); ev.Phase != abi.OutcomeResult || string(ev.Payload) != `"rendered"` {
		t.Fatalf("section completion = outcome %d %s", ev.Phase, ev.Payload)
	}
	if target.ConversationId != 3 || target.Cwd == nil || *target.Cwd != "/work" {
		t.Fatalf("env target = %+v", target)
	}
	if envs.Get(7) != env.ExecutionEnv(built) || envs.Get(8) != nil {
		t.Fatal("the environment belongs to its task alone")
	}
}

func TestSectionThatRendersNothingCompletesWithNull(t *testing.T) {
	section := &durable.PromptSection{Key: "k", Render: func(context.Context, durable.PromptInput) (*string, error) { return nil, nil }}
	core, host, _ := sectionRig(t, nil, []*durable.PromptSection{section}, 0)
	if ev := completion(t, core, host, 2); ev.Phase != abi.OutcomeResult || string(ev.Payload) != `null` {
		t.Fatalf("section completion = outcome %d %s", ev.Phase, ev.Payload)
	}
}

func TestEnvFailureIsAThrownOutcomeAndStoresNothing(t *testing.T) {
	core, host, envs := sectionRig(t, func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) {
		return nil, errors.New("no sandbox")
	}, nil, 0)
	if ev := completion(t, core, host, 1); ev.Phase != abi.OutcomeThrown || string(ev.Payload) != `{"name":"Error","message":"no sandbox"}` {
		t.Fatalf("env completion = outcome %d %s", ev.Phase, ev.Payload)
	}
	if envs.Get(7) != nil {
		t.Fatal("a failed build stores no environment")
	}
}

func TestSectionRenderFailureIsThrownAndAnUnknownSectionDiscardsTheHost(t *testing.T) {
	failing := &durable.PromptSection{Key: "k", Render: func(context.Context, durable.PromptInput) (*string, error) { return nil, errors.New("render broke") }}
	core, host, _ := sectionRig(t, nil, []*durable.PromptSection{failing}, 0)
	if ev := completion(t, core, host, 2); ev.Phase != abi.OutcomeThrown || !strings.Contains(string(ev.Payload), "render broke") {
		t.Fatalf("section completion = outcome %d %s", ev.Phase, ev.Payload)
	}
	_, host2, _ := sectionRig(t, nil, []*durable.PromptSection{failing}, 5)
	waitFor(t, "discard", func() bool { return host2.Err() != nil })
	if !strings.Contains(host2.Err().Error(), "names section 5 of 1") {
		t.Fatalf("err = %v", host2.Err())
	}
}

func TestAnEnvEffectThatBuildsNothingClearsTheTasksEnvironment(t *testing.T) {
	calls := 0
	envs := &driver.Envs{Reader: func() durable.DocumentReader { return nil }, Build: func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) {
		calls++
		if calls == 1 {
			return &fakeEnv{name: "first"}, nil
		}
		return nil, nil
	}}
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		switch {
		case ev.Kind == abi.EventOpen:
			return &abi.Step{Effects: []abi.Effect{{ID: 1, Kind: abi.EffectEnv, Payload: []byte(`{"conversationId":3,"taskId":7}`)}}}
		case ev.Kind == abi.EventHookDone && ev.ID == 1:
			return &abi.Step{Effects: []abi.Effect{{ID: 2, Kind: abi.EffectEnv, Payload: []byte(`{"conversationId":3,"taskId":7}`)}}}
		}
		return nil
	}}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	host := sqlhost.New(sqlhost.Options{Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectEnv: envs.Handler()}})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventOpen, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	completion(t, core, host, 1)
	completion(t, core, host, 2)
	if envs.Get(7) != nil {
		t.Fatal("the second build produced nothing: the task has no environment")
	}
}
