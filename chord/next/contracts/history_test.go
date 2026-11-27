package contracts_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestGrantableAndFlagsAndMessageData(t *testing.T) {
	flagLabel := contracts.MessageData{Namespace: "app", Key: "export", Params: []any{}}
	if !contracts.IsMessageData(flagLabel) {
		t.Fatal("valid MessageData refused")
	}
	if contracts.IsMessageData(contracts.MessageData{Namespace: "app", Key: "x", Params: nil}) {
		t.Fatal("nil Params should be refused")
	}

	c, err := contracts.Remote("notes", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: serverAddress,
		Admission:   ptr(contracts.Obj(contracts.Prop("role", contracts.Str()))),
		State: []contracts.RemoteStateEntry{
			contracts.StateEntry("title", contracts.Str()),
			contracts.StateEntry("draft", contracts.NewGrantable(contracts.Str())),
		},
		Methods: []contracts.RemoteMethodEntry{
			contracts.Meth("add", contracts.NewGrantable(contracts.NewMethod(contracts.Str()))),
		},
		Events: []contracts.RemoteEventEntry{
			contracts.EventEntry("ping", contracts.NewGrantable(contracts.Str())),
		},
		Flags: []contracts.FlagEntry{
			contracts.Flag("export", flagLabel),
		},
	})
	if err != nil {
		t.Fatalf("Remote failed: %v", err)
	}
	v, err := contracts.CurrentVersion(c)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	wantGrants := []string{"add", "draft", "export", "ping"}
	if !slices.Equal(v.Grants, wantGrants) {
		t.Fatalf("Grants = %v, want %v", v.Grants, wantGrants)
	}
	idx := contracts.GrantIndexes(v, map[string]bool{"draft": true, "export": true})
	if !slices.Equal(idx, []int{1, 2}) {
		t.Fatalf("GrantIndexes = %v, want [1 2]", idx)
	}
	gm := contracts.GrantedMembers(v, map[string]bool{"draft": true})
	if !gm.State.Has("title") || !gm.State.Has("draft") || gm.Methods.Has("add") || gm.Events.Has("ping") {
		t.Fatalf("GrantedMembers mismatch: state=%v methods=%v events=%v", gm.State.Keys(), gm.Methods.Keys(), gm.Events.Keys())
	}
	order := contracts.MemberOrder(gm)
	if !slices.Equal(order.State, []string{"draft", "title"}) {
		t.Fatalf("MemberOrder.State = %v", order.State)
	}

	// Flag label is NOT in the fingerprint
	c2, err := contracts.Remote("notes", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: serverAddress,
		State: []contracts.RemoteStateEntry{
			contracts.StateEntry("title", contracts.Str()),
			contracts.StateEntry("draft", contracts.NewGrantable(contracts.Str())),
		},
		Methods: []contracts.RemoteMethodEntry{
			contracts.Meth("add", contracts.NewGrantable(contracts.NewMethod(contracts.Str()))),
		},
		Events: []contracts.RemoteEventEntry{
			contracts.EventEntry("ping", contracts.NewGrantable(contracts.Str())),
		},
		Flags: []contracts.FlagEntry{
			contracts.Flag("export", contracts.MessageData{Namespace: "other", Key: "different", Params: []any{"x"}}),
		},
	})
	if err != nil {
		t.Fatalf("Remote c2 failed: %v", err)
	}
	if c.Fingerprint != c2.Fingerprint {
		t.Fatalf("flag label changed fingerprint: %s vs %s", c.Fingerprint, c2.Fingerprint)
	}

	// Admission with "granted" field is refused
	_, err = contracts.Remote("bad.admit", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: serverAddress,
		Admission:   ptr(contracts.Obj(contracts.Prop("granted", contracts.Arr(contracts.Str())))),
	})
	if err == nil {
		t.Fatal("expected admission with granted field to be refused")
	}

	// QueryArguments refuses bytes()
	_, err = contracts.QueryArguments(contracts.Obj(contracts.Prop("b", contracts.Bytes())), "q.args")
	if err == nil {
		t.Fatal("expected QueryArguments to refuse bytes()")
	}
	_ = delta.JsonObjectOf
}

func ptr[T any](v T) *T { return &v }
