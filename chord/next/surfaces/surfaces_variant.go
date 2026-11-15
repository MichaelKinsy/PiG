package surfaces

import "github.com/MichaelKinsy/PiG/chord/next/host"

// Ports packages/chord/src/surfaces/variant.ts
//
// A remote reference as a UI reads it in one variant (decision "C15 stands; the UI bindings follow the variant"): the
// reference narrowed to that variant while it is live in it, nothing otherwise. What React's `useVariant` and the TUI scope's
// `variant` return, each with its own subscription to the reference's state: a held reference's variant is unbound until it
// binds and changes while it is held (an admission's other allowed value binds another variant), and it keeps its last variant
// while it loads, so the comparison alone would leave an editor's controls up for a reader. References themselves get no such
// helper (C15): plain code compares the variant.
//
// Forced Go differences from upstream (language mechanics): Pi's `VariantRef`, `VariantOf` and `InVariant` are type-level
// (the reference typed as the variant's members, a variant the type does not declare refused at compile time); a Go
// *host.Reference is one type, so LiveIn takes the variant by name and a name the type does not declare is never live.

// LiveIn is ref when it is live and bound in variant, else nil: no reference, loading (also between two variants), failed
// (denied too), ended, or another variant. True of the moment it is read: no guarantee across a wait.
func LiveIn(ref *host.Reference, variant string) *host.Reference {
	if ref == nil || ref.State().Value().Kind != host.RefStateLive {
		return nil
	}
	// Variant fails on a released reference: read only while live.
	if bound, ok, err := ref.Variant(); err != nil || !ok || bound != variant {
		return nil
	}
	return ref
}
