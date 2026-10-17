package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func TestTypeScriptDeclarationsPinUpstreamAndExposePiGLogin(t *testing.T) {
	root := findModuleRoot(t)
	packageData, err := os.ReadFile(filepath.Join(root, "extensions", "sdk-ts", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		PeerDependencies map[string]string `json:"peerDependencies"`
		DevDependencies  map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(packageData, &manifest); err != nil {
		t.Fatal(err)
	}
	lockData, err := os.ReadFile(filepath.Join(root, "extensions", "sdk-ts", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}

	// The constant, not the text of pigversion.go, so a PI_UPSTREAM_REF run (a Go overlay of that file) checks its own pin.
	// npm dependencies name the package version (1.1.0), not a commit pin's display version (1.1.0+42a3497d).
	wantVersion := pigversion.UpstreamPackageVersion
	const piPackage = "@earendil-works/pi-coding-agent"
	if got := manifest.PeerDependencies[piPackage]; got != wantVersion {
		t.Errorf("peer dependency %s = %q, want %q", piPackage, got, wantVersion)
	}
	if got := manifest.DevDependencies[piPackage]; got != wantVersion {
		t.Errorf("development dependency %s = %q, want %q", piPackage, got, wantVersion)
	}
	if got := lock.Packages["node_modules/"+piPackage].Version; got != wantVersion {
		t.Errorf("locked dependency %s = %q, want %q", piPackage, got, wantVersion)
	}

	declarations, err := os.ReadFile(filepath.Join(root, "extensions", "sdk-ts", "index.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`export type * from "@earendil-works/pi-coding-agent"`,
		`interface PiGLoginDefinition`,
		`setLogin(definition: PiGLoginDefinition): Promise<void>`,
		`interface PiGSpriteDefinition`,
		`registerSprite(definition: PiGSpriteDefinition): Promise<void>`,
	} {
		if !strings.Contains(string(declarations), required) {
			t.Errorf("TypeScript declarations do not contain %q", required)
		}
	}
}
