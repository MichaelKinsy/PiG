package contracts

// Ports packages/chord/src/next/contracts/history.ts. Upstream's history is a type-level structure (Variants, Step,
// History, CurrentStep, StepAt, OlderVersions, ChangesAfter, Increment); all of it is designed out. At runtime a
// contract holds its versions (VersionData: the number, the fingerprint, the variants and the changes that produced
// it), and accepts reads the older versions' changes directly. What remains of the history types as runtime helpers
// is MemberNames, VariantsWith and MethodAt.

// ChangeSide is the side of a member an edit changed.
type ChangeSide string

// The sides of an edit.
const (
	SideState  ChangeSide = "state"
	SideArgs   ChangeSide = "args"
	SideResult ChangeSide = "result"
)

// Change is what one edit changed: one side of one member in one variant. An edit that changed several variants
// makes one Change per variant.
type Change struct {
	Variant string
	Member  string
	Side    ChangeSide
}

// MemberSide names a part of a variant: its state members, its methods or its keyed members (upstream's
// `keyof VariantShape`).
type MemberSide string

// The parts of a variant.
const (
	MemberSideState   MemberSide = "state"
	MemberSideMethods MemberSide = "methods"
	MemberSideKeyed   MemberSide = "keyed"
)

// MemberNames lists the names of the members of side in any variant, each once, in order of first appearance.
func MemberNames(variants *NamedMap[*VariantData], side MemberSide) []string {
	seen := map[string]bool{}
	var names []string
	for _, variant := range variants.All() {
		var keys []string
		switch side {
		case MemberSideState:
			keys = variant.State.Keys()
		case MemberSideMethods:
			keys = variant.Methods.Keys()
		case MemberSideKeyed:
			keys = variant.Keyed.Keys()
		}
		for _, name := range keys {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

// VariantsWith lists the variants that have method member, in order.
func VariantsWith(variants *NamedMap[*VariantData], member string) []string {
	var names []string
	for name, variant := range variants.All() {
		if variant.Methods.Has(member) {
			names = append(names, name)
		}
	}
	return names
}

// MethodAt is method member (one signature in every variant that has it), from the first variant that has it.
func MethodAt(variants *NamedMap[*VariantData], member string) (*MethodData, bool) {
	for _, variant := range variants.All() {
		if method, ok := variant.Methods.Get(member); ok {
			return method, true
		}
	}
	return nil, false
}
