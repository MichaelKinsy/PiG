package testenv

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

const rootSDKModulePath = "github.com/MichaelKinsy/PiG/extensions/sdk"

// RootSDKRequirement returns the version of the extension SDK module that the root go.mod at root requires, spelled as in the file ("v0.7.1-0.20261108053302-4e194449dcd7"). A test that writes a go.mod requiring the replaced SDK or root module must take the version from it, not from pigversion.PigVersion or a literal: between releases the root requires the SDK at a pseudo-version, and a generated module that requires an older one stops `go build` with "updates to go.mod needed". The in-tree modules are held to the same requirement by make module-pins, which also fails on a test that writes such a go.mod without this function.
func RootSDKRequirement(root string) (string, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	file, err := modfile.Parse(path, data, nil)
	if err != nil {
		return "", err
	}
	for _, requirement := range file.Require {
		if requirement.Mod.Path == rootSDKModulePath {
			return requirement.Mod.Version, nil
		}
	}
	return "", fmt.Errorf("%s does not require %s", path, rootSDKModulePath)
}
