// UpstreamVersion and UpstreamCommit are the exact upstream Pi pins. The CLI,
// parity runner, source mirror, generated inventories, and Pig Porter consume
// them. Change both together, in coding/pigversion/pigversion.go, then run
// the upgrade workflow in piglets/porter/skills/pig-porter/SKILL.md.
package coding

import "github.com/MichaelKinsy/PiG/coding/pigversion"

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = pigversion.PigVersion

// Version is PiG's one displayed version: its own release line with the Pi
// release it ports as semver build metadata (for example 0.2.0+0.87.1). Semver
// precedence ignores build metadata, so Version sorts exactly as PigVersion.
// Self-update comparisons, release tags, and the Piglet compatibility check
// still use PigVersion itself.
const Version = pigversion.Version

// UpstreamCommit is the exact Pi release commit mirrored for UpstreamVersion.
const UpstreamCommit = pigversion.UpstreamCommit

// UpstreamVersion is the Pi release whose behavior PiG targets.
const UpstreamVersion = pigversion.UpstreamVersion

// UpstreamReviewedVersion is the most recent pin whose parity ledgers carry
// reviewed dispositions. Leap ledgers (the semantic interface delta, the
// behavior-input mapping carry-forward, and the upstream-sync manifest) cover
// every change from this version to UpstreamVersion. Advance it only after
// those ledgers have no pending rows.
const UpstreamReviewedVersion = "0.84.0"
