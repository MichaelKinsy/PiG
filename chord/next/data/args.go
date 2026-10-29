package data

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/next/data/args.ts: a provider's binding (its dispatch table per accepted version) and the
// per-call argument check.
//
// Go forced differences: Provider<C> and CheckedArgs<C, M> are generic over a contract's static types (ArgsOf,
// Static<...>); a Go contract is a *contracts.Contract whose argument types are runtime schemas, so arguments are
// Value. Upstream's `throw` is a returned error; `result` and a `call` replacement return errors for the same reason.

// Value is JSON plus byte leaves: nil, bool, float64, string, []byte, []any, *delta.JsonObject. Same-host values are
// plain data and immutable once handed over, by convention.
type Value = any

// CheckedArgsKind is the kind of CheckedArgs.
type CheckedArgsKind string

const (
	// CheckedMethod is a method's arguments in the current shape.
	CheckedMethod CheckedArgsKind = "method"
	// CheckedCall is the old arguments for the provider's own `call` replacement.
	CheckedCall CheckedArgsKind = "call"
)

// CheckedArgs is the result of CheckArgs: a method's arguments in the current shape with the result mapping back to
// the caller's version (kind "method"; Result nil is upstream's absent `result`), or the old arguments for the
// provider's own `call` replacement (kind "call"; L4 dispatches it).
type CheckedArgs struct {
	Kind CheckedArgsKind
	Args Value
	// Result maps the current result back to the caller's version; method only, nil when absent.
	Result contracts.Mapping
	// Call is the provider's replacement; call only.
	Call contracts.CallFunc
}

// Provider is a provider's binding of a contract with the older versions it accepts.
type Provider struct {
	Contract *contracts.Contract
	Accepted []contracts.AcceptedVersion
	table    dispatch
}

// BindProvider binds a contract and the app's accepts table (validated once, kept as it is) into a provider. Builds the
// dispatch table: accepted version -> method -> schema and mapping; nothing is compiled yet.
func BindProvider(contract *contracts.Contract, accepts contracts.AcceptsTable) (*Provider, error) {
	checked, err := contracts.CheckAccepts(contract, accepts)
	if err != nil {
		return nil, err
	}
	contractData, err := contracts.ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	versions := contractData.Versions
	current := versions[len(versions)-1]
	table := dispatch{current.Version: dispatchEntries(current, nil)}
	for _, accepted := range checked.Versions {
		var data *contracts.VersionData
		for index := range versions {
			if versions[index].Version == accepted.Version {
				data = &versions[index]
				break
			}
		}
		var mappings []contracts.Entry[contracts.MethodMapping]
		for _, entry := range accepts {
			if entry.Name == strconv.Itoa(accepted.Version) {
				mappings = entry.Value.Methods
			}
		}
		table[accepted.Version] = dispatchEntries(*data, mappings)
	}
	return &Provider{Contract: contract, Accepted: checked.Versions, table: table}, nil
}

// dispatch is accepted version -> method -> entry.
type dispatch map[int]map[string]*dispatchEntry

// dispatchKind is what a dispatch entry does with a call.
type dispatchKind int

const (
	dispatchRefuse dispatchKind = iota
	dispatchCall
	dispatchMethod
)

// dispatchEntry is one method of one accepted version. The validator is made on the first check.
type dispatchEntry struct {
	kind      dispatchKind
	args      *contracts.Snapshot
	call      contracts.CallFunc
	mapArgs   contracts.Mapping
	mapResult contracts.Mapping

	once      sync.Once
	validator *Validator
	err       error
}

// check is the entry's validator, compiled once.
func (entry *dispatchEntry) check(ctx context.Context) (*Validator, error) {
	entry.once.Do(func() { entry.validator, entry.err = ValidatorFor(entry.args, ctx) })
	return entry.validator, entry.err
}

func dispatchEntries(version contracts.VersionData, mappings []contracts.Entry[contracts.MethodMapping]) map[string]*dispatchEntry {
	entries := map[string]*dispatchEntry{}
	// A method has one signature in every variant that has it.
	for _, variant := range version.Variants.All() {
		if variant.Methods == nil {
			continue
		}
		for member, method := range variant.Methods.All() {
			var mapping *contracts.MethodMapping
			for index := range mappings {
				if mappings[index].Name == member {
					mapping = &mappings[index].Value
				}
			}
			entries[member] = dispatchEntryOf(method.Args, mapping)
		}
	}
	return entries
}

func dispatchEntryOf(args *contracts.Snapshot, mapping *contracts.MethodMapping) *dispatchEntry {
	switch {
	case mapping != nil && mapping.Refuse:
		return &dispatchEntry{kind: dispatchRefuse}
	case mapping != nil && mapping.Call != nil:
		return &dispatchEntry{kind: dispatchCall, args: args, call: mapping.Call}
	case mapping != nil:
		return &dispatchEntry{kind: dispatchMethod, args: args, mapArgs: mapping.Args, mapResult: mapping.Result}
	}
	return &dispatchEntry{kind: dispatchMethod, args: args}
}

// CheckArgs checks a call's arguments against the provider's schema for the caller's bound version: three map lookups,
// one compiled check, at most one direct mapping old -> current (never for current-version calls). A method without
// arguments gets `null` (its internal and wire form). Unknown method: `invalid/member`; version not accepted or
// refused: `contract_mismatch`.
func CheckArgs(provider *Provider, method string, raw any, boundVersion int, ctx context.Context) (CheckedArgs, error) {
	name := provider.Contract.Name
	methods, ok := provider.table[boundVersion]
	if !ok {
		return CheckedArgs{}, NewChordError(ChordErrorInit{Code: CodeContractMismatch, Message: fmt.Sprintf("%s v%d is not accepted", name, boundVersion)}, nil)
	}
	entry, ok := methods[method]
	if !ok {
		return CheckedArgs{}, Invalid(ReasonMember, fmt.Sprintf(`%s v%d has no method "%s"`, name, boundVersion, method))
	}
	if entry.kind == dispatchRefuse {
		return CheckedArgs{}, NewChordError(ChordErrorInit{Code: CodeContractMismatch, Message: fmt.Sprintf(`%s v%d refuses "%s"`, name, boundVersion, method)}, nil)
	}
	validator, err := entry.check(ctx)
	if err != nil {
		return CheckedArgs{}, err
	}
	if err := Check(validator, raw, ReasonArgs, ctx); err != nil {
		return CheckedArgs{}, err
	}
	// The check above establishes the argument type; mapping output is trusted.
	var args Value = raw
	if entry.kind == dispatchCall {
		return CheckedArgs{Kind: CheckedCall, Args: args, Call: entry.call}, nil
	}
	if entry.mapArgs != nil {
		if args, err = entry.mapArgs(delta.JsonValue(args)); err != nil {
			return CheckedArgs{}, err
		}
	}
	return CheckedArgs{Kind: CheckedMethod, Args: args, Result: entry.mapResult}, nil
}
