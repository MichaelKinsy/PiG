// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.5.0"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "1.1.0"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "abe508e1b89912adde45528136c3221eb69acdd7"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
