package surfaces

import "github.com/MichaelKinsy/PiG/chord/next/host"

// Ports packages/chord/src/surfaces/live.ts (slice 15 §1.5): a remote reference as a UI reads it: the reference while
// it is live, and while it loads again after it last settled live (a rebind, a link drop: its tools stay up and its
// calls park; the reference's own history, not what a UI read), nothing otherwise (no reference, its first load, failed
// or denied, ended): what React's `useLive` and `TuiScope.Live` return.

// LiveRef is a remote reference, held or got: its state, the names it was granted, and whether it last settled live.
type LiveRef interface {
	State() host.RefStateWatchable
	Granted() (map[string]bool, error)
	WasLive() bool
}

// LiveGrant returns ref's granted names while ref is live, or loading after it last settled live (by its own history,
// not by what a UI read of it); else nil. True of the moment it is read: no guarantee across a wait.
func LiveGrant(ref LiveRef) map[string]bool {
	if ref == nil {
		return nil
	}
	kind := ref.State().Value().Kind
	// Granted fails on a released reference: read only while live or loading.
	if kind == host.RefStateLive || (kind == host.RefStateLoading && ref.WasLive()) {
		granted, err := ref.Granted()
		if err != nil {
			return nil
		}
		return granted
	}
	return nil
}
