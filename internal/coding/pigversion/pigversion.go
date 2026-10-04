// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.4.0"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "1.0.0"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "a13d35a742c6ef8462812a28fbe1d8c8b7431c32"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
