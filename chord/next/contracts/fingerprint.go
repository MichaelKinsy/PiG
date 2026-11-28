package contracts

import (
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/contracts/fingerprint.ts (the normal form of a contract version and its portable
// 64-bit hash; not a security boundary).

// NormalFormVersion is the version of the normal form; changing the normal form changes it and is a link-protocol
// change. It stays 2 although a remote contract's node changed in slice 15 (members, grantable ones and flags instead
// of variants): documents' stored fingerprints carry it, and a remote node of another shape changes the remote
// fingerprints by itself.
const NormalFormVersion = 2

// Materialize materializes a version with its fingerprint: Hash64 of `JSON.stringify({ normalFormVersion, value })`
// over the normal form `{ kind: "local", params, members }` or `{ kind: "remote", params, members, grantable, flags }`
// (the grantable members and the flag names sorted; a flag's label is not in it).
func Materialize(kind Kind, params *ParamsData, version int, members *MembersData, grantable map[string]bool, flags []string, changes []Change) (VersionData, error) {
	grantableNames := make([]string, 0, len(grantable))
	for name, ok := range grantable {
		if ok {
			grantableNames = append(grantableNames, name)
		}
	}
	slices.SortFunc(grantableNames, compare16)
	flagNames := slices.Clone(flags)
	slices.SortFunc(flagNames, compare16)

	fingerprint, err := FingerprintOf(NormalForm(kind, params, members, grantableNames, flagNames))
	if err != nil {
		return VersionData{}, err
	}
	grants := make([]string, 0, len(grantableNames)+len(flagNames))
	grants = append(grants, grantableNames...)
	grants = append(grants, flagNames...)
	slices.SortFunc(grants, compare16)

	grantableCopy := map[string]bool{}
	if len(grantable) > 0 {
		grantableCopy = maps.Clone(grantable)
	}
	return VersionData{
		Version:     version,
		Fingerprint: fingerprint,
		Members:     members,
		Grantable:   grantableCopy,
		Grants:      grants,
		Changes:     slices.Clone(changes),
		cache:       &versionCache{grantedCache: map[string]*MembersData{}},
	}, nil
}

// NormalForm is the normal form a version's fingerprint hashes: `{ kind: "local", params, members }` or
// `{ kind: "remote", params, members, grantable, flags }`.
func NormalForm(kind Kind, params *ParamsData, members *MembersData, grantableNames, flagNames []string) *delta.JsonObject {
	normal := delta.JsonObjectOf("kind", string(kind), "params", params.Normal, "members", membersNormal(members))
	if kind == KindRemote {
		g := make([]any, len(grantableNames))
		for i, name := range grantableNames {
			g[i] = name
		}
		f := make([]any, len(flagNames))
		for i, name := range flagNames {
			f[i] = name
		}
		normal.Set("grantable", g)
		normal.Set("flags", f)
	}
	return normal
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

func membersNormal(members *MembersData) delta.JsonValue {
	entries := make([]normalEntry, 0, members.Queries.Len()+members.State.Len()+members.Events.Len()+members.Keyed.Len()+members.Methods.Len())
	for member, queries := range members.Queries.All() {
		named := make([]normalEntry, 0, queries.Args.Len())
		for query, schema := range queries.Args.All() {
			named = append(named, normalEntry{query, schema.Normal})
		}
		entries = append(entries, normalEntry{member, delta.JsonObjectOf("queries", queries.Item.Normal, "args", sortedDict(named))})
	}
	for member, schema := range members.State.All() {
		entries = append(entries, normalEntry{member, delta.JsonObjectOf("state", schema.Normal)})
	}
	for member, schema := range members.Events.All() {
		entries = append(entries, normalEntry{member, delta.JsonObjectOf("event", schema.Normal)})
	}
	for member, schema := range members.Keyed.All() {
		node := delta.JsonObjectOf("keyed", schema.Normal)
		if schema.Key != nil {
			node = delta.JsonObjectOf("keyed", schema.Normal, "key", schema.Key.Kind)
		}
		entries = append(entries, normalEntry{member, node})
	}
	for member, method := range members.Methods.All() {
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
