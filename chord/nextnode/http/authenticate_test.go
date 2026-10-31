package http

import (
	"context"
	"errors"
	"reflect"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Tests of the authenticators: asked in contributor order, the first principal wins, a throwing one reported and
// skipped, none: anonymous. Ports test/next-node/http/authenticate.test.ts.

// authenticatorRequest is the request they are asked about (its headers are never read).
var authenticatorRequest HttpRequest = nil

type authenticatorFailure struct {
	plugin string
	err    error
}

func authenticatorTable(values map[string]Authenticator, order ...string) registries.RegistryTable[Authenticator] {
	table := registries.RegistryTable[Authenticator]{}
	for _, plugin := range order {
		table = append(table, registries.RegistryEntry[Authenticator]{Plugin: plugin, Version: 1, Value: values[plugin]})
	}
	return table
}

func TestAuthenticatorsAreAskedInContributorOrderTheFirstPrincipalWinsAThrowingOneIsReportedAndSkipped(t *testing.T) {
	var asked []string
	owner, guest := principalOwner(), principalGuest("g")
	broken := errors.New("broken")
	table := authenticatorTable(map[string]Authenticator{
		"@app/a": func(HttpRequest, context.Context) (data.Principal, error) {
			asked = append(asked, "a")
			return nil, nil
		},
		"@app/b": func(HttpRequest, context.Context) (data.Principal, error) {
			asked = append(asked, "b")
			return nil, broken
		},
		"@app/c": func(HttpRequest, context.Context) (data.Principal, error) {
			asked = append(asked, "c")
			return owner, nil
		},
		"@app/d": func(HttpRequest, context.Context) (data.Principal, error) {
			asked = append(asked, "d")
			return guest, nil
		},
	}, "@app/a", "@app/b", "@app/c", "@app/d")
	composed, err := composeAuthenticators(table, registries.ComposeContext{})
	if err != nil {
		t.Fatal(err)
	}
	var failed []authenticatorFailure
	principal := authenticate(composed, authenticatorRequest, func(plugin string, err error) {
		failed = append(failed, authenticatorFailure{plugin, err})
	}, chordctx.Background())
	if principal != owner {
		t.Fatalf("principal = %v, want the owner", principal)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("asked = %v, want %v", asked, want)
	}
	if len(failed) != 1 || failed[0].plugin != "@app/b" || failed[0].err != broken {
		t.Fatalf("failed = %v, want [@app/b broken]", failed)
	}
}

// Sol's second recheck C: a fresh authenticator started after the request was cancelled.
func TestAuthenticatorsStartNoAuthenticatorOnceTheContextIsAborted(t *testing.T) {
	calls := 0
	table := authenticatorTable(map[string]Authenticator{
		"@app/a": func(HttpRequest, context.Context) (data.Principal, error) {
			calls++
			return nil, nil
		},
	}, "@app/a")
	composed, err := composeAuthenticators(table, registries.ComposeContext{})
	if err != nil {
		t.Fatal(err)
	}
	aborted, cancel := chordctx.WithCancel(chordctx.Background())
	cancel(nil)
	if principal := authenticate(composed, authenticatorRequest, func(string, error) {}, aborted); principal != nil {
		t.Fatalf("principal = %v, want none", principal)
	}
	if calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
}

func TestAuthenticatorsGiveNoPrincipalWhenNoneKnowsTheRequest(t *testing.T) {
	if principal := authenticate(nil, authenticatorRequest, func(string, error) {}, chordctx.Background()); principal != nil {
		t.Fatalf("principal = %v, want none", principal)
	}
}
