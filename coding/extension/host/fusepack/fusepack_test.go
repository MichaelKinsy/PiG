package fusepack

import (
	"net"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// TestFusepackResolver proves the contract generated code relies on: factory
// registration is lazy, and the resolver matches by extension name while
// rejecting names this Piglet Binary did not fuse.
func TestFusepackResolver(t *testing.T) {
	orig := fusedServes
	t.Cleanup(func() { fusedServes = orig; subprocess.SetFusedResolver(nil) })
	fusedServes = map[string]func(net.Conn) error{}

	// Empty Piglet Binary: Register installs nothing.
	Register()

	factoryCalls := 0
	registerFactory("demo", func() *sdk.Extension {
		factoryCalls++
		return sdk.New("demo")
	})
	Register()
	if factoryCalls != 0 {
		t.Fatalf("factory called during registration: %d", factoryCalls)
	}

	var r resolver
	serve, ok := r.FusedServe(subprocess.ExtConfig{Name: "demo"})
	if !ok || serve == nil {
		t.Fatal("resolver did not resolve fused 'demo'")
	}
	if _, ok := r.FusedServe(subprocess.ExtConfig{Name: "other"}); ok {
		t.Fatal("resolver matched a name this Piglet Binary did not fuse")
	}
}

func TestRegisterFactoryRecoversConstructorPanic(t *testing.T) {
	orig := fusedServes
	t.Cleanup(func() { fusedServes = orig })
	fusedServes = map[string]func(net.Conn) error{}
	registerFactory("broken", func() *sdk.Extension { panic("boom") })

	hostConn, extConn := net.Pipe()
	defer func() { _ = hostConn.Close() }()
	err := fusedServes["broken"](extConn)
	if err == nil || !strings.Contains(err.Error(), `construct fused extension "broken": boom`) {
		t.Fatalf("serve error = %v", err)
	}
}

func TestRegisterFactoryRejectsNilExtension(t *testing.T) {
	orig := fusedServes
	t.Cleanup(func() { fusedServes = orig })
	fusedServes = map[string]func(net.Conn) error{}
	registerFactory("broken", func() *sdk.Extension { return nil })

	hostConn, extConn := net.Pipe()
	defer func() { _ = hostConn.Close() }()
	err := fusedServes["broken"](extConn)
	if err == nil || !strings.Contains(err.Error(), "factory returned nil") {
		t.Fatalf("serve error = %v", err)
	}
}

// A registry generated for a Piglet built with `build.fusedReload: source` records each fused member's source digest; the resolver
// answers with it, and has none for a member the build baked none for.
func TestRegisterSourceRecordsTheBakedDigest(t *testing.T) {
	t.Cleanup(func() { delete(fusedSources, "demo") })
	if _, ok := (resolver{}).BakedSourceDigest("demo"); ok || BakedSource("demo") {
		t.Fatal("a member has a baked digest before any was registered")
	}
	registerSource("demo", "0123abcd")
	if digest, ok := (resolver{}).BakedSourceDigest("demo"); !ok || digest != "0123abcd" || !BakedSource("demo") {
		t.Fatalf("baked digest = %q, %t", digest, ok)
	}
	if _, ok := (resolver{}).BakedSourceDigest("other"); ok || BakedSource("other") {
		t.Fatal("a member the build baked nothing for has a digest")
	}
	var _ subprocess.FusedSourceResolver = resolver{}
}
