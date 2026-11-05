package subprocess

import "net"

// FusedResolver maps an extension config to an in-process serve func compiled
// into the binary. A forged Piglet Binary (fused Piglet Binary) installs one whose serve funcs
// are each fused extension's factory().RunWithConn; the resolver is nil in stock
// pig, so LoadAll routes every extension through the subprocess cell path
// unchanged (D31).
type FusedResolver interface {
	// FusedServe returns the in-process serve func for cfg if this Piglet Binary
	// compiled that extension in, and false otherwise.
	FusedServe(cfg ExtConfig) (serve func(net.Conn) error, ok bool)
}

var fusedResolver FusedResolver

// SetFusedResolver installs the process-wide fused-extension resolver. Called
// once at startup by a Piglet Binary's generated registration; nil (the default)
// disables fusion. Not safe for concurrent use with LoadAll.
func SetFusedResolver(r FusedResolver) { fusedResolver = r }

// RunsFused reports whether the host serves cfg from the Piglet Binary's own code, in this process, as LoadAll and a reload do. Such an
// extension reads no source and builds nothing, so it needs no staged SDK. It is false in stock pig, which installs no resolver.
func RunsFused(cfg ExtConfig) bool {
	if fusedResolver == nil {
		return false
	}
	_, ok := fusedResolver.FusedServe(cfg)
	return ok
}
