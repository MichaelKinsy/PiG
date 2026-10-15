package subprocess

import (
	"errors"
	"testing"
)

// A library entry point (coding.DiscoverAndLoadExtensions) installs the staged-SDK check the pig command registers, built for the
// configuration root it uses, and installs none when no program registered one.
func TestDefaultStagedSDKVerifierIsBuiltForTheConfigRootOrAbsent(t *testing.T) {
	t.Cleanup(func() { stagedSDKVerifierFactory.Store(nil) })
	stagedSDKVerifierFactory.Store(nil)
	if DefaultStagedSDKVerifier("/root") != nil {
		t.Fatal("a verifier exists although none was registered")
	}
	var roots []string
	sentinel := errors.New("stale")
	SetDefaultStagedSDKVerifier(func(root string) func(string, string) error {
		roots = append(roots, root)
		return func(dir, buildType string) error { return errors.Join(sentinel, errors.New(dir+" "+buildType)) }
	})
	verify := DefaultStagedSDKVerifier("/root")
	if verify == nil || len(roots) != 1 || roots[0] != "/root" {
		t.Fatalf("verifier %v built for roots %v, want one for /root", verify != nil, roots)
	}
	if err := verify("/staged", "go"); !errors.Is(err, sentinel) {
		t.Fatalf("verify = %v, want the registered check's error", err)
	}
}
