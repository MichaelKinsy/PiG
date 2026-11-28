package contracts

// Ports packages/chord/src/contracts/history.ts. Upstream's history is a type-level structure (MembersShape, Step,
// History, CurrentStep, StepAt, OlderVersions, ChangesAfter, Increment); all of it is designed out. At runtime a
// contract holds its versions (VersionData: the number, the fingerprint, the members, grantable, grants and the changes
// that produced it), and accepts reads the older versions' changes directly.

// ChangeSide is the side of a member an edit changed.
type ChangeSide string

// The sides of an edit; SideKeyed is a local contract's keyed member (slice 7b), SideEvent an event member.
const (
	SideState  ChangeSide = "state"
	SideArgs   ChangeSide = "args"
	SideResult ChangeSide = "result"
	SideKeyed  ChangeSide = "keyed"
	SideEvent  ChangeSide = "event"
)

// Change is what one edit changed: one side of one member.
type Change struct {
	Member string
	Side   ChangeSide
}

// MemberSide names a part of a contract's members: its state members, its methods, its keyed members, its event
// members or its queries members.
type MemberSide string

// The parts of a contract's members.
const (
	MemberSideState   MemberSide = "state"
	MemberSideMethods MemberSide = "methods"
	MemberSideKeyed   MemberSide = "keyed"
	MemberSideEvents  MemberSide = "events"
	// MemberSideQueries is a local contract's queries members (slice 14 §3.1).
	MemberSideQueries MemberSide = "queries"
)
