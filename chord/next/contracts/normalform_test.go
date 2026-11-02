package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Not upstream's: NormalForm is the extraction of the form Materialize hashes (fingerprint.ts), so a contract can cross a
// process boundary as data and be rebuilt by Pi's own code. Oracle: the fingerprint the contract has, which is the hash of
// this form.

func normalText(t *testing.T, normal *delta.JsonObject) string {
	t.Helper()
	text, err := jsstring.MarshalJSON(normal)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestNormalFormOfARemoteContractIsWhatItsFingerprintHashes(t *testing.T) {
	note := func() contracts.Schema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
	notes := remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str())),
		variant("reader", state(contracts.Prop("notes", note()))),
		variant("editor", state(contracts.Prop("notes", note())), contracts.Meth("add", contracts.NewMethod(note(), contracts.Str()))))
	declared, err := contracts.ContractDataOf(notes)
	if err != nil {
		t.Fatal(err)
	}
	current := declared.Versions[len(declared.Versions)-1]
	normal := contracts.NormalForm(declared.Kind, declared.Params, current.Variants)
	if normal.Value("kind") != "remote" || normal.Value("members") != nil {
		t.Fatalf("normal form = %s, want kind remote with variants and no members", normalText(t, normal))
	}
	variants, ok := normal.Value("variants").(*delta.JsonObject)
	if !ok {
		t.Fatalf("variants = %T", normal.Value("variants"))
	}
	if keys := variants.Keys(); len(keys) != 2 || keys[0] != "editor" || keys[1] != "reader" {
		t.Fatalf("variants are not a sorted dictionary: %v", keys)
	}
	fingerprint, err := contracts.FingerprintOf(normal)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != current.Fingerprint {
		t.Fatalf("the normal form hashes to %s, the contract's fingerprint is %s", fingerprint, current.Fingerprint)
	}
}

func TestNormalFormOfALocalContractHasItsMembersAsASortedDictionary(t *testing.T) {
	thing := local(t, "test.thing", 1, props(contracts.Prop("id", contracts.Str())),
		contracts.Mem("zeta", contracts.Str()), contracts.Mem("alpha", contracts.Int()))
	declared, err := contracts.ContractDataOf(thing)
	if err != nil {
		t.Fatal(err)
	}
	current := declared.Versions[len(declared.Versions)-1]
	normal := contracts.NormalForm(declared.Kind, declared.Params, current.Variants)
	if normal.Value("kind") != "local" || normal.Value("variants") != nil {
		t.Fatalf("normal form = %s, want kind local with members and no variants", normalText(t, normal))
	}
	members, ok := normal.Value("members").(*delta.JsonObject)
	if !ok {
		t.Fatalf("members = %T", normal.Value("members"))
	}
	if keys := members.Keys(); len(keys) != 2 || keys[0] != "alpha" || keys[1] != "zeta" {
		t.Fatalf("members are not a sorted dictionary: %v", keys)
	}
	fingerprint, err := contracts.FingerprintOf(normal)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != current.Fingerprint {
		t.Fatalf("the normal form hashes to %s, the contract's fingerprint is %s", fingerprint, current.Fingerprint)
	}
}
