package host

import (
	"reflect"
	"testing"
)

// Pi contract.ts:61-85 and 124-160: remote(name, def) and local(name, def) return { scope, name, version, input, def,
// description, fingerprint }, where description is { name, scope, version, input, variants } (a local contract's one
// variant is LOCAL_VARIANT "", contract.ts:59) and fingerprint is hash64(canonicalJson(description)) (contract.ts:119).
func TestContractTokensCarryPiFields(t *testing.T) {
	input := tObject("session", tString())
	def := RemoteDef{Version: 3, Input: input, Host: NewHostNamed("server"), Variants: Fields[VariantDef]{
		{"viewer", VariantDef{State: Fields[Schema]{{"view", view}}}},
		{"writer", VariantDef{State: Fields[Schema]{{"view", view}}, Methods: Fields[MethodSpec]{{"send", Method(tString(), tNumber())}}}},
	}}
	remote := Remote("t.remote", def)
	if remote.Scope != ScopeRemote || remote.Name != "t.remote" || remote.Version != 3 || !reflect.DeepEqual(remote.Input, input) ||
		remote.Local != nil || remote.Remote == nil || !reflect.DeepEqual(*remote.Remote, def) {
		t.Fatalf("remote token = %+v", remote)
	}
	wantRemote := Description{Name: "t.remote", Scope: "remote", Version: 3, Input: input, Host: HostNamed{Name: "server"}, Variants: Fields[Fields[MemberDescription]]{
		{"viewer", Fields[MemberDescription]{{"view", MemberDescription{Kind: "state", Schema: view}}}},
		{"writer", Fields[MemberDescription]{{"view", MemberDescription{Kind: "state", Schema: view}}, {"send", MemberDescription{Kind: "method", Args: tString(), Result: tNumber()}}}},
	}}
	if !reflect.DeepEqual(remote.Description, wantRemote) {
		t.Fatalf("remote description = %+v", remote.Description)
	}
	if remote.Fingerprint != Hash64(CanonicalJSON(wantRemote)) || len(remote.Fingerprint) != 16 {
		t.Fatalf("remote fingerprint = %s", remote.Fingerprint)
	}

	localDef := LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"count", tNumber()}, {"harness", Opaque("h@1")}, {"bump", Method(tNull(), tNumber())},
	}}
	local := Local("t.local", localDef)
	if local.Scope != ScopeLocal || local.Name != "t.local" || local.Version != 1 || !reflect.DeepEqual(local.Input, tObject()) ||
		local.Remote != nil || local.Local == nil || !reflect.DeepEqual(*local.Local, localDef) {
		t.Fatalf("local token = %+v", local)
	}
	wantLocal := Description{Name: "t.local", Scope: "local", Version: 1, Input: tObject(), Variants: Fields[Fields[MemberDescription]]{
		{LocalVariant, Fields[MemberDescription]{
			{"count", MemberDescription{Kind: "state", Schema: tNumber()}},
			{"harness", MemberDescription{Kind: "opaque", ID: "h@1"}},
			{"bump", MemberDescription{Kind: "method", Args: tNull(), Result: tNumber()}},
		}},
	}}
	if !reflect.DeepEqual(local.Description, wantLocal) || local.Fingerprint != Hash64(CanonicalJSON(wantLocal)) {
		t.Fatalf("local description = %+v, fingerprint %s", local.Description, local.Fingerprint)
	}
	if local.Fingerprint == remote.Fingerprint {
		t.Fatal("two descriptions share a fingerprint")
	}
}
