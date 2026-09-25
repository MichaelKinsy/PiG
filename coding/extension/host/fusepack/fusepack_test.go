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
