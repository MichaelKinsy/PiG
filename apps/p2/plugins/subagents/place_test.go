package subagents

import (
	"context"
	"regexp"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports "a sandboxed caller places subagents only in its sandbox" of test/p2/sandboxes.test.ts (Pi main c23b9ec1).
func TestASandboxedCallerPlacesSubagentsOnlyInItsSandbox(t *testing.T) {
	slayer, box1 := "slayer", "box1"
	where := locator{
		locate: func(context.Context, durable.ConversationId, durable.DocumentReader) (p2runtime.Location, error) {
			return p2runtime.Location{Env: &box1, Cwd: "/workspace"}, nil
		},
		sandbox: func(_ context.Context, location p2runtime.Location) (string, error) {
			if location.Env != nil && *location.Env == "box1" {
				return "box1", nil
			}
			return "", nil
		},
		directory: func(_ context.Context, target p2runtime.LocationInput, _ p2runtime.Location) (p2runtime.Location, string) {
			path := target.Path
			if path == "" {
				path = "/workspace"
			}
			return p2runtime.Location{Env: target.Env, Cwd: path}, ""
		},
	}
	caller := p2runtime.Location{Env: &box1, Cwd: "/workspace"}
	tmp := "/tmp"
	for _, args := range []placement{{envSet: true, env: &slayer}, {envSet: true}, {envSet: true, cwd: &tmp}} {
		_, problem, err := placeTarget(context.Background(), where, caller, args)
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`only work inside it`).MatchString(problem) {
			t.Errorf("placeTarget(%+v) = %q", args, problem)
		}
	}
	inside := "/workspace/a"
	placed, problem, err := placeTarget(context.Background(), where, caller, placement{cwd: &inside})
	if err != nil || problem != "" || !p2runtime.SameLocation(placed, p2runtime.Location{Env: &box1, Cwd: "/workspace/a"}) {
		t.Errorf("placeTarget inside = %+v, %q, %v", placed, problem, err)
	}
	// An unsandboxed caller may place a subagent in a sandbox (which then confines that subagent).
	placed, problem, err = placeTarget(context.Background(), where, p2runtime.Location{Env: &slayer, Cwd: "/"}, placement{envSet: true, env: &box1})
	if err != nil || problem != "" || !p2runtime.SameLocation(placed, p2runtime.Location{Env: &box1, Cwd: "/workspace"}) {
		t.Errorf("placeTarget into a sandbox = %+v, %q, %v", placed, problem, err)
	}
}
