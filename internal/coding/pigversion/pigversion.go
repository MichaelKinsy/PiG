// Package pigversion provides PiG and upstream Pi version pins to packages
// that cannot import coding.
package pigversion

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = "0.3.1"

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = "1.0.1"

// UpstreamCommit is the exact Pi release commit for UpstreamVersion.
const UpstreamCommit = "a7229ddc21810d6245105978033b7df645ecc2f7"

// Version combines the PiG release line with the upstream Pi release as semver
// build metadata.
const Version = PigVersion + "+" + UpstreamVersion
