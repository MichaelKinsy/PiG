//go:build !pig_strip_docs

package codingagent

// The pig binary links internal/pigdocs, which registers this build's documentation bundle with configroot; these tests run with the same
// registration, as a binary that strips docs does not.
import _ "github.com/MichaelKinsy/PiG/internal/pigdocs"
