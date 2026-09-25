package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

	upstreamData, err := os.ReadFile(filepath.Join(root, "coding", "pigversion", "pigversion.go"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`const UpstreamVersion = "([^"]+)"`).FindSubmatch(upstreamData)
	if len(match) != 2 {
		t.Fatal("coding.UpstreamVersion not found")
	}
	wantVersion := string(match[1])
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
	} {
		if !strings.Contains(string(declarations), required) {
			t.Errorf("TypeScript declarations do not contain %q", required)
		}
	}
}
