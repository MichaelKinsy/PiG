// UpstreamVersion, UpstreamPackageVersion and UpstreamCommit are the exact upstream Pi pin.
// The CLI, parity runner, source mirror, generated inventories, and Pig Porter consume
// them. The pin lives in .upstream/pin; `make pin-bump PI_UPSTREAM_REF=<tag|commit>`
// regenerates internal/coding/pigversion/pigversion.go and every other derived file,
// then the upgrade workflow in piglets/porter/skills/pig-porter/SKILL.md applies.
package coding

import "github.com/MichaelKinsy/PiG/internal/coding/pigversion"

// PigVersion is PiG's release line. It advances independently of the upstream
// Pi target.
const PigVersion = pigversion.PigVersion

// Version is PiG's one displayed version: its own release line with the Pi
// release it ports as semver build metadata (for example 0.2.0+0.87.1). Semver
// precedence ignores build metadata, so Version sorts exactly as PigVersion.
// Self-update comparisons, release tags, and the Piglet compatibility check
// still use PigVersion itself.
const Version = pigversion.Version

// UpstreamCommit is the exact Pi commit of the pin.
const UpstreamCommit = pigversion.UpstreamCommit

// UpstreamRef is the pinned tag or commit of Pi.
const UpstreamRef = pigversion.UpstreamRef

// UpstreamVersion is the pinned Pi build's display and ledger version: the
// release version for a tag, the release version plus +<commit[:8]> for a commit
// (Pi 1.1.0+42a3497d). Ledger files and parity inventories are named by it.
const UpstreamVersion = pigversion.UpstreamVersion

// UpstreamPackageVersion is the version Pi's package.json files and `pi --version`
// report. It equals UpstreamVersion for a release tag.
const UpstreamPackageVersion = pigversion.UpstreamPackageVersion

// UpstreamReviewedVersion is the most recent pin whose parity ledgers carry
// reviewed dispositions. Leap ledgers (the semantic interface delta, the
// behavior-input mapping carry-forward, and the upstream-sync manifest) cover
// every change from this version to UpstreamVersion. Advance it only after
// those ledgers have no pending rows.
const UpstreamReviewedVersion = "0.84.0"
