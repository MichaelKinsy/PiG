// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.4.0"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "1.0.2"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "cd32f7725fdbddbaecdff5b1e68491563394e0ca"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
