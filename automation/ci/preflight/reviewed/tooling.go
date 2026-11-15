// SPDX-License-Identifier: MIT

package reviewed

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// ProvenanceFile is the path of the file whose [[tooling]] table lists the CI tooling directories.
const ProvenanceFile = "test/testdata-provenance.toml"

// Tooling puts a directory of PiG-owned CI tooling outside the expected-value rule: the program there is not ported
// from Pi, so there is no Pi behavior to cite for a changed expectation. The list is reviewed like the ledger: every
// entry names its reason, and only a directory under automation/ qualifies, so product code never leaves the rule. The
// line lint (test/parity/cmd/testprune) and the pre-flight's test-expectation rule both read it through this package.
type Tooling struct {
	// Prefix is a directory of the repository, with a trailing slash, such as automation/ci/preflight/.
	Prefix string `toml:"prefix"`
	Reason string `toml:"reason"`
}

// ParseTooling reads the [[tooling]] table of the provenance file's text.
func ParseTooling(text string) ([]Tooling, error) {
	var f struct {
		Tooling []Tooling `toml:"tooling"`
	}
	if _, err := toml.Decode(text, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", ProvenanceFile, err)
	}
	return f.Tooling, nil
}

// InTooling reports whether file lies under a listed tooling directory, and the prefix that holds it.
func InTooling(list []Tooling, file string) (prefix string, ok bool) {
	for _, e := range list {
		if e.Prefix != "" && strings.HasPrefix(file, e.Prefix) {
			return e.Prefix, true
		}
	}
	return "", false
}
