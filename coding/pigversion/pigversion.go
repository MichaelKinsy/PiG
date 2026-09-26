// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.2.1"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "0.87.1"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "f07218c4d4bbc12bef056a7058c3dd49dfe41abe"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
