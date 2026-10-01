// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.3.1"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "0.99.2"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "005af57d88ee23b33778f343a9595b32e67ff788"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
