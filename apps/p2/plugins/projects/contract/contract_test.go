package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contract fingerprints equal Pi's (see sessions/contract).
func TestProjectsContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/projects/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, contract := range map[string]*contracts.Contract{"ProjectService": ProjectService, "Projects": Projects} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("%s: Pi exports no such contract", name)
			continue
		}
		if contract.Name != want.Name || contract.Version != want.Version || contract.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, contract.Name, contract.Version, contract.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}

func ptr(text string) *string { return &text }

func TestBelowAndPlaceOnFromThePathsAlone(t *testing.T) {
	type below struct {
		path, root, rest string
		ok               bool
	}
	for _, c := range []below{
		// `~/rel`: `/rel` at segment boundaries after the (unknown, non-empty) home.
		{"/home/me/projects/pi/pi", "~/projects/pi/pi", "", true},
		{"/Users/someone/projects/pi/pi/apps/p2", "~/projects/pi/pi", "apps/p2", true},
		{"/home/me/projects/pi/pi2", "~/projects/pi/pi", "", false},
		{"/home/me/old-projects/pi/pi", "~/projects/pi/pi", "", false},
		{"/projects/pi/pi", "~/projects/pi/pi", "", false},
		// Absolute places: the path itself or below it.
		{"/srv/app/src", "/srv/app", "src", true},
		{"/srv/app/src", "/srv/app/", "src", true},
		{"/srv/application", "/srv/app", "", false},
		// Windows machines: either separator, the subpath as written there.
		{`C:\Users\me\code\pi\src\lib`, "~/code/pi", `src\lib`, true},
		{`C:\work\pi\src`, `C:\work\pi`, "src", true},
		// `~` contains every directory, its subpath unknown without the home.
		{"/home/me/x", "~", "", true},
		{"/a/b", "/a/b/", "", true},
		{"/a/bc", "/a/b", "", false},
	} {
		rest, ok := Below(c.path, c.root)
		if rest != c.rest || ok != c.ok {
			t.Errorf("Below(%q, %q) = %q, %v; want %q, %v", c.path, c.root, rest, ok, c.rest, c.ok)
		}
	}
	project := Project{Name: "pi", Places: []ProjectPlace{{Env: ptr("mac"), Path: "~/projects/pi/pi"}, {Path: "~/projects/pi/pi"}}}
	for _, c := range []struct {
		name string
		want *ProjectPlace
	}{
		{"mac", &project.Places[0]},
		{"local", &project.Places[1]},
		{"David-pi", &project.Places[1]},
		{"gpu", nil},
	} {
		got, ok := PlaceOn(project, c.name, "david-pi")
		if c.want == nil && ok || c.want != nil && (!ok || got.Path != c.want.Path || (got.Env == nil) != (c.want.Env == nil)) {
			t.Errorf("PlaceOn(%s) = %+v, %v; want %+v", c.name, got, ok, c.want)
		}
	}
}
