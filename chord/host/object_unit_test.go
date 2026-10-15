package host

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// fakeReadable is a readable-like state source (events and subscribe) that is not a cell.
type fakeReadable struct{}

func (fakeReadable) Value() any                                { return nil }
func (fakeReadable) Subscribe(func(any)) func()                { return func() {} }
func (fakeReadable) Events(func(event StateEvent[any])) func() { return func() {} }

func chordCode(t *testing.T, err error) ChordErrorCode {
	t.Helper()
	var chordError *ChordError
	if !errors.As(err, &chordError) {
		t.Fatalf("error %v is not a ChordError", err)
	}
	return chordError.Code
}

// object.ts:122-140: an open that completes after its signal aborted closes the value it produced at once and fails
// aborted; the resource stays closed and out of the open order.
func TestObjectResourceOpenAfterAbortClosesTheValue(t *testing.T) {
	host := &HostImpl{}
	signal, abort := context.WithCancelCause(context.Background())
	var closed []any
	resource := newResourceImpl(host, "db", ResourceDeclaration[any]{
		Open: func(ResourceOpenOptions) (any, error) {
			abort(errors.New("rollback"))
			return "conn", nil
		},
		Close: func(value any) error { closed = append(closed, value); return nil },
	}, nil)
	var order []*ResourceImpl
	err := resource.open(signal, &order)
	if chordCode(t, err) != CodeAborted || err.Error() != "aborted: resource db opened after abort" {
		t.Fatalf("open error = %v", err)
	}
	if !slices.Equal(closed, []any{"conn"}) || resource.state != "closed" || len(order) != 0 {
		t.Fatalf("closed %v, state %q, order %v", closed, resource.state, order)
	}
}

// object.ts:113-121, 128-150: a resource is not_ready until opened; open objects it in the open order once; close
// removes it, hands the value to close and makes it not_ready again; a failed open leaves it closed with the error.
func TestObjectResourceOpenCloseLifecycle(t *testing.T) {
	host := &HostImpl{}
	opens := 0
	var closed []any
	resource := newResourceImpl(host, "db", ResourceDeclaration[any]{
		Open:  func(ResourceOpenOptions) (any, error) { opens++; return opens, nil },
		Close: func(value any) error { closed = append(closed, value); return nil },
	}, nil)
	if _, err := resource.Value(); chordCode(t, err) != CodeNotReady {
		t.Fatalf("value before open: %v", err)
	}
	var order []*ResourceImpl
	for range 2 {
		if err := resource.open(context.Background(), &order); err != nil {
			t.Fatal(err)
		}
	}
	if value, err := resource.Value(); err != nil || value != 1 || opens != 1 || len(order) != 1 {
		t.Fatalf("value %v (%v), opens %d, order %v", value, err, opens, order)
	}
	if err := resource.close(&order); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(closed, []any{1}) || len(order) != 0 {
		t.Fatalf("closed %v, order %v", closed, order)
	}
	if _, err := resource.Value(); chordCode(t, err) != CodeNotReady {
		t.Fatalf("value after close: %v", err)
	}
	resource.markUnavailable()
	if _, err := resource.Value(); chordCode(t, err) != CodeUnavailable {
		t.Fatalf("value when unavailable: %v", err)
	}

	failing := newResourceImpl(host, "bad", ResourceDeclaration[any]{
		Open: func(ResourceOpenOptions) (any, error) { return nil, errors.New("refused") },
	}, nil)
	if err := failing.open(context.Background(), &order); err == nil || err.Error() != "refused" || failing.state != "closed" {
		t.Fatalf("failed open: %v, state %q", err, failing.state)
	}
}

// object.ts:1323-1350: a remote implementation names a known variant and only its members; methods are functions, state
// members are sources or readables, opaque members are present.
func TestObjectValidateImplementation(t *testing.T) {
	remote := Remote("t.v", RemoteDef{Version: 1, Input: Schema{"type": "object"}, Host: NewHostNamed("server"), Variants: Fields[VariantDef]{
		{"main", VariantDef{State: Fields[Schema]{{"count", Schema{"type": "number"}}}, Methods: Fields[MethodSpec]{{"add", Method(Schema{"type": "number"}, Schema{"type": "number"})}}}},
	}})
	local := Local("t.l", LocalDef{Version: 1, Input: Schema{"type": "object"}, Members: Fields[any]{{"native", Opaque("t.native@1")}}})
	add := methodFunc(func(any, Ctx) (any, error) { return nil, nil })
	cases := []struct {
		name           string
		contract       *Contract
		implementation Implementation
		message        string
	}{
		{"valid remote", remote, chordjson.ObjectOf("variant", "main", "add", add, "count", fakeReadable{}), ""},
		{"nil", remote, nil, "invalid/implementation: t.v: factory must return an object"},
		{"unknown variant", remote, chordjson.ObjectOf("variant", "other"), "invalid/implementation: t.v: unknown variant other"},
		{"no variant", remote, chordjson.ObjectOf("add", add), "invalid/implementation: t.v: unknown variant undefined"},
		{"extra member", remote, chordjson.ObjectOf("variant", "main", "add", add, "count", fakeReadable{}, "x", 1), "invalid/implementation: t.v: x is not in variant main"},
		{"method missing", remote, chordjson.ObjectOf("variant", "main", "count", fakeReadable{}), "invalid/implementation: t.v: add must be a method"},
		{"state not a source", remote, chordjson.ObjectOf("variant", "main", "add", add, "count", 3), "invalid/implementation: t.v: count must be a cell, a derivation or a held object's state member"},
		{"valid local", local, chordjson.ObjectOf("native", struct{}{}), ""},
		{"opaque missing", local, chordjson.ObjectOf(), "invalid/implementation: t.l: native is missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, err := makeService(tc.contract, nil, ServeOptions{}, "")
			if err != nil {
				t.Fatal(err)
			}
			err = validateImplementation(service, tc.implementation)
			if tc.message == "" {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
}

// object.ts:282-286: a ctx refuses work from another host, after its call settled (expired), and once aborted.
func TestObjectCtxCheck(t *testing.T) {
	host, other := &HostImpl{}, &HostImpl{}
	signal, abort := context.WithCancelCause(context.Background())
	ctx := newCtxImpl(host, ctxFields{signals: []context.Context{signal}, rootID: "r", codeVersion: 1})
	if err := ctx.check(host); err != nil {
		t.Fatal(err)
	}
	if err := ctx.check(other); err == nil || err.Error() != "invalid/foreign_ctx: ctx belongs to another host" {
		t.Fatalf("foreign: %v", err)
	}
	abort(errors.New("stop"))
	if err := ctx.check(host); err == nil || err.Error() != "aborted: ctx aborted" || context.Cause(signal).Error() != "stop" {
		t.Fatalf("aborted: %v", err)
	}
	ctx.close()
	if err := ctx.check(host); err == nil || err.Error() != "aborted/expired: ctx expired: its call already settled" {
		t.Fatalf("expired: %v", err)
	}
}

// object.ts:328-340: raceSignal fails aborted when the signal is already aborted or aborts while waiting, and
// otherwise returns the promise's outcome.
func TestObjectRaceSignal(t *testing.T) {
	host := &HostImpl{}
	aborted, abort := context.WithCancelCause(context.Background())
	abort(errors.New("why"))
	promise, resolve, _ := newPromise[int](&host.loop)
	if _, err := raceSignal(promise, aborted); chordCode(t, err) != CodeAborted || !errors.Is(err, errors.Unwrap(err)) {
		t.Fatalf("pre-aborted: %v", err)
	}
	waiting, stop := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { _, err := raceSignal(promise, waiting); done <- err }()
	stop(errors.New("late"))
	if err := <-done; chordCode(t, err) != CodeAborted || err.Error() != "aborted: call aborted" {
		t.Fatalf("aborted while waiting: %v", err)
	}
	resolve(7)
	if value, err := raceSignal(promise, context.Background()); err != nil || value != 7 {
		t.Fatalf("resolved: %v %v", value, err)
	}
}
