package contracts

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/contracts/fingerprint.ts (the normal form of a contract version and its portable
// 64-bit hash; not a security boundary).

// NormalFormVersion is the version of the normal form; changing the normal form changes it and is a link-protocol
// change.
const NormalFormVersion = 2

// Materialize materializes a version with its fingerprint: Hash64 of `JSON.stringify({ normalFormVersion, value })`
// over the normal form `{ kind: "remote", params, variants }` or `{ kind: "local", params, members }`.
func Materialize(kind Kind, params *ParamsData, version int, variants *NamedMap[*VariantData], changes []Change) (VersionData, error) {
	normal := delta.JsonObjectOf("kind", string(kind), "params", params.Normal)
	if kind == KindLocal {
		normal.Set("members", variantNormal(variants.Value(LocalVariant)))
	} else {
		entries := make([]normalEntry, 0, variants.Len())
		for name, variant := range variants.All() {
			entries = append(entries, normalEntry{name, variantNormal(variant)})
		}
		normal.Set("variants", sortedDict(entries))
	}
	fingerprint, err := FingerprintOf(normal)
	if err != nil {
		return VersionData{}, err
	}
	return VersionData{Version: version, Fingerprint: fingerprint, Variants: variants, Changes: slices.Clone(changes)}, nil
}

// FingerprintOf is Hash64 of `JSON.stringify({ normalFormVersion, value: normal })`: the fingerprint of a normal form.
func FingerprintOf(normal delta.JsonValue) (string, error) {
	text, err := jsstring.MarshalJSON(delta.JsonObjectOf("normalFormVersion", float64(NormalFormVersion), "value", normal))
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	return Hash64(string(text)), nil
}

type normalEntry struct {
	name  string
	value any
}

// variantNormal is the members as a sorted dictionary of `{ state }`, `{ args, result }` or `{ keyed }` (slice 6a
// §12: a node of its own, so contracts without keyed members keep their fingerprints and NormalFormVersion stays).
func variantNormal(variant *VariantData) delta.JsonValue {
	entries := make([]normalEntry, 0, variant.State.Len()+variant.Keyed.Len()+variant.Methods.Len())
	for member, schema := range variant.State.All() {
		entries = append(entries, normalEntry{member, delta.JsonObjectOf("state", schema.Normal)})
	}
	for member, schema := range variant.Keyed.All() {
		entries = append(entries, normalEntry{member, delta.JsonObjectOf("keyed", schema.Normal)})
	}
	for member, method := range variant.Methods.All() {
		entries = append(entries, normalEntry{member, delta.JsonObjectOf("args", method.Args.Normal, "result", method.Result.Normal)})
	}
	return sortedDict(entries)
}

func sortedDict(entries []normalEntry) delta.JsonValue {
	slices.SortStableFunc(entries, func(a, b normalEntry) int { return compare16(a.name, b.name) })
	object := delta.NewJsonObject(len(entries))
	for _, entry := range entries {
		object.Set(entry.name, entry.value)
	}
	return object
}

// Hash64 is 16 hex characters from two 32-bit FNV-1a lanes (different offset bases and primes) over the UTF-8 bytes.
func Hash64(text string) string {
	var low uint32 = 0x811c9dc5
	var high uint32 = 0xcbf29ce4
	for _, b := range jsstring.ToUTF8(text) {
		low = (low ^ uint32(b)) * 0x01000193
		high = (high ^ uint32(b)) * 0x01000197
	}
	return fmt.Sprintf("%08x%08x", high, low)
}
