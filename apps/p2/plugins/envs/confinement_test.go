package envs

import (
	"context"
	"errors"
	"fmt"
	"testing"

	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// locatedAt are the execution environments of a session whose conversations work at the locations at: a conversation not
// listed works where the session does (conversation 1). Whether an environment is a sandbox is decided by its configured
// type, as the worker's does.
type locatedAt struct {
	at   map[durable.ConversationId]p2runtime.Location
	envs EnvsConfig
}

func (environments locatedAt) Session() p2runtime.Location { return environments.at[1] }

func (environments locatedAt) Locate(_ context.Context, id durable.ConversationId, _ durable.DocumentReader) (p2runtime.Location, error) {
	if location, ok := environments.at[id]; ok {
		return location, nil
	}
	return environments.at[1], nil
}

func (locatedAt) At(context.Context, p2runtime.Location) (durableenv.ExecutionEnv, error) {
	return nil, errors.New("no environment opens in this test")
}

func (environments locatedAt) Sandbox(_ context.Context, env *string) (string, error) {
	if env != nil && IsSandbox(environments.envs, *env) {
		return *env, nil
	}
	return "", nil
}

// committedDocs are the committed documents a reader returns, by document kind and the conversation they belong to.
type committedDocs struct {
	durable.DocumentReader
	values map[string]durable.JsonObject
}

func (read committedDocs) SnapshotErased(_ context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	return read.values[fmt.Sprintf("%s:%v", token.AnyDefinition().Kind, args[0])], nil
}

// Ports "confinement is decided per conversation from its location; one-shot agents inherit their caller's" of
// test/p2/sandboxes.test.ts (Pi main c23b9ec1).
func TestConfinementIsDecidedPerConversationFromItsLocationAndOneShotAgentsInheritTheirCallers(t *testing.T) {
	configured, err := ParseEnvs(`{"slayer":{"type":"ssh","host":"slayer.example"},"box1":{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":["registry.npmjs.org","*.github.com"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	slayer, box1 := "slayer", "box1"
	at := map[durable.ConversationId]p2runtime.Location{1: {Env: &slayer, Cwd: "/"}, 2: {Env: &box1, Cwd: "/workspace"}}
	environments := locatedAt{at: at, envs: configured}
	ctx := context.Background()
	for id, want := range map[durable.ConversationId]string{1: "", 2: "box1"} {
		got, err := p2runtime.ConversationConfinement(ctx, environments, id, nil)
		if err != nil || got != want {
			t.Errorf("ConversationConfinement(%d) = %q, %v; want %q", id, got, err, want)
		}
	}
	location := delta.JsonObjectOf("env", "box1", "cwd", "/workspace")
	read := committedDocs{values: map[string]durable.JsonObject{
		subagentscontract.SubagentsDoc.AnyDefinition().Kind + ":1": delta.JsonObjectOf(
			"agents", delta.JsonObjectOf("worker", delta.JsonObjectOf("conversationId", float64(2), "location", location)),
			"reporters", delta.NewJsonObject(0)),
		subagentscontract.InheritedLocationDoc.AnyDefinition().Kind + ":3": delta.JsonObjectOf("location", location),
	}}
	for id, want := range map[durable.ConversationId]*p2runtime.Location{2: new(at[2]), 3: new(at[2]), 4: nil} {
		got, err := subagentscontract.OwnLocation(ctx, read, id)
		if err != nil {
			t.Fatalf("OwnLocation(%d): %v", id, err)
		}
		if (got == nil) != (want == nil) || got != nil && !p2runtime.SameLocation(*got, *want) {
			t.Errorf("OwnLocation(%d) = %+v, want %+v", id, got, want)
		}
	}
}
