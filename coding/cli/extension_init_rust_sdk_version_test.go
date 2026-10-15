// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Pig resolves the Rust scaffold's registry-form pig-sdk requirement to the staged SDK through a crates-io patch, and Cargo applies a patch only when the
// patched crate's version satisfies the requirement (caret semantics: 0.x requires the same minor). The scaffold must therefore accept the SDK crate the
// running binary stages, or `pig extension init --lang rust` writes an extension that cannot build.
func TestRustScaffoldRequirementAcceptsTheStagedSDKVersion(t *testing.T) {
	req := regexp.MustCompile(`(?m)^pig-sdk = "([^"]+)"`).FindStringSubmatch(rustCargoTemplate("x"))
	if req == nil {
		t.Fatal("the Rust scaffold has no registry-form pig-sdk requirement")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "extensions", "sdk-rs", "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^version = "([^"]+)"`).FindStringSubmatch(string(data))
	if match == nil {
		t.Fatal("extensions/sdk-rs/Cargo.toml has no package version")
	}
	have := match[1]
	parse := func(v string) []int {
		var out []int
		for _, part := range strings.SplitN(v, ".", 3) {
			n, err := strconv.Atoi(part)
			if err != nil {
				t.Fatalf("version %q: %v", v, err)
			}
			out = append(out, n)
		}
		for len(out) < 3 {
			out = append(out, 0)
		}
		return out
	}
	r, h := parse(req[1]), parse(have)
	compatible := r[0] == h[0] && (r[0] != 0 || r[1] == h[1]) && (h[1] > r[1] || (h[1] == r[1] && h[2] >= r[2]))
	if !compatible {
		t.Fatalf("scaffold requires pig-sdk %q, which does not accept the staged SDK crate %s", req[1], have)
	}
}
