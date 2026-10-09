package nodepath

import (
	"encoding/json"
	"os"
	"testing"
)

type joinCase struct {
	Flavor    string   `json:"flavor"`
	Args      []string `json:"args"`
	Want      string   `json:"want"`
	Normalize *string  `json:"normalize"`
	Basename  *string  `json:"basename"`
}

// TestJoinNormalizeBasenameMatchNode compares both flavors with Node 24's path module (testdata/generate_join.mjs).
func TestJoinNormalizeBasenameMatchNode(t *testing.T) {
	data, err := os.ReadFile("testdata/join_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []joinCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 5000 {
		t.Fatalf("case table is too small: %d", len(file.Cases))
	}
	for _, c := range file.Cases {
		join, normalize, basename := PosixJoin, PosixNormalize, PosixBasename
		if c.Flavor == "win32" {
			join, normalize, basename = Win32Join, Win32Normalize, Win32Basename
		}
		if got := join(c.Args...); got != c.Want {
			t.Errorf("%s join(%q) = %q, Node gives %q", c.Flavor, c.Args, got, c.Want)
		}
		if c.Normalize != nil {
			if got := normalize(c.Args[0]); got != *c.Normalize {
				t.Errorf("%s normalize(%q) = %q, Node gives %q", c.Flavor, c.Args[0], got, *c.Normalize)
			}
		}
		if c.Basename != nil {
			if got := basename(c.Args[0]); got != *c.Basename {
				t.Errorf("%s basename(%q) = %q, Node gives %q", c.Flavor, c.Args[0], got, *c.Basename)
			}
		}
	}
}
