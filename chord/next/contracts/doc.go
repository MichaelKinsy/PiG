// Package contracts ports packages/chord/src/next/contracts: the contracts of Chord's layers. An app declares remote
// and local object types as contracts (Remote, Local), evolves them version by version (Evolve), lists the older
// versions a provider still serves (CheckAccepts), names its hosts by kind and address (NewHostKind, Address), and
// describes state, arguments and results with schema constructors (Str, Obj, NewMethod, ...).
//
// Schemas are JSON Schema data, as TypeBox builds them: a TSchema holds the schema node (an ordered
// *delta.JsonObject, or a boolean) and TypeBox's non-enumerable "~optional" marker. Every schema an app hands to a
// contract is checked against Chord's vocabulary and copied once into a Snapshot, which carries the normal form that
// fingerprints are computed from. Snapshots, params data and contract data are Chord-owned and held by pointer, so a
// pointer is a stable cache key.
//
// Differences forced by Go, each recorded where it is declared:
//   - TypeScript's type-level machinery (Static, ParamsOf, Implementation<C>, AcceptsTable<C>, the history types, the
//     branded generic Contract<S>) has no runtime form and is designed out; its runtime meaning lives in the data a
//     Contract holds.
//   - An object literal whose key order is observable is an ordered slice of Entry values (Property, Params,
//     VariantEntry, MethodEntry, LocalEntry); the entries are read in the order JavaScript enumerates an object's own
//     keys. A nil slice is an absent field, an empty non-nil one is `{}`.
//   - A function that shares its name with a type is renamed: `method` is NewMethod, `hostKind` is NewHostKind,
//     `LOCAL` is LocalVariant (Local declares a local contract).
//   - index.ts and internal.ts only re-export (the public and the internal surface of one package): designed out;
//     every name is exported from this package.
//   - A thrown Error is a returned error with upstream's message.
package contracts
