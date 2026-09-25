package codingagent

import pig "github.com/MichaelKinsy/PiG"

// getBundledChangelogForTest exists so changelog_test.go can read the
// embedded CHANGELOG.md without importing the root pig package
// directly. Test-only indirection.
func getBundledChangelogForTest() string { return pig.Changelog }
