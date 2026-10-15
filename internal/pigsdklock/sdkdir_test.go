package pigsdklock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStagedSDKDirUsesRegisteredBaseAndHashPrefix(t *testing.T) {
	RegisterSDK("test-lang", "sdk-test", func() (string, error) { return "0123456789abcdef0123", nil })
	root := t.TempDir()
	got, err := StagedSDKDir(root, "test-lang")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "state", "pigsdk", "sdk-test-0123456789abcdef"); got != want {
		t.Fatalf("StagedSDKDir = %q, want %q", got, want)
	}
}

func TestStagedSDKDirLaterRegistrationReplacesEarlier(t *testing.T) {
	RegisterSDK("test-replace", "sdk-old", func() (string, error) { return "0123456789abcdef", nil })
	RegisterSDK("test-replace", "sdk-new", func() (string, error) { return "fedcba9876543210", nil })
	root := t.TempDir()
	got, err := StagedSDKDir(root, "test-replace")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "state", "pigsdk", "sdk-new-fedcba9876543210"); got != want {
		t.Fatalf("StagedSDKDir = %q, want %q", got, want)
	}
}

// A language no SDK registered for means a program built on the extension host without package pigsdk: the error names the import that fixes it.
func TestStagedSDKDirRejectsUnregisteredLanguage(t *testing.T) {
	dir, err := StagedSDKDir(t.TempDir(), "test-unregistered")
	if err == nil {
		t.Fatalf("StagedSDKDir = %q, want error", dir)
	}
	if want := "this program links no test-unregistered extension SDK; import github.com/MichaelKinsy/PiG/coding/extension/pigsdk to embed and stage the SDKs"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestStagedSDKDirPropagatesHashError(t *testing.T) {
	hashErr := errors.New("hash failed")
	RegisterSDK("test-hash-error", "sdk-test", func() (string, error) { return "", hashErr })
	if _, err := StagedSDKDir(t.TempDir(), "test-hash-error"); !errors.Is(err, hashErr) {
		t.Fatalf("StagedSDKDir error = %v, want %v", err, hashErr)
	}
}

func TestStagedSDKDirRejectsShortHash(t *testing.T) {
	RegisterSDK("test-short-hash", "sdk-test", func() (string, error) { return "0123456789abcde", nil })
	if dir, err := StagedSDKDir(t.TempDir(), "test-short-hash"); err == nil {
		t.Fatalf("StagedSDKDir = %q, want error", dir)
	}
}
