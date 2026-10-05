// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.4.0"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "1.0.3"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "d78dc83d633229d12f8b79631384c4c2717c399f"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
