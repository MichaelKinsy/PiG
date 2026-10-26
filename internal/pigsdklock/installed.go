package pigsdklock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pig additive (D201): a packager installs the extension SDKs read-only beside the binary instead of staging them under the config root.

// MarkerFile records, in each staged or installed SDK directory, the content hash of the SDK the directory holds.
const MarkerFile = ".pig-sdk-version"

// installedSDKRoot is the read-only directory a packager installed this binary's extension SDKs and TypeScript extension runtime in, each
// under the content-addressed name it would stage as (`pig reload --install-sdks <dir>` writes them). A packager sets it at link time:
//
//	-ldflags "-X github.com/MichaelKinsy/PiG/internal/pigsdklock.installedSDKRoot=<dir>"
//
// Empty, the default, stages the SDKs under the config root. Set, the SDKs are only read and verified there, and nothing is staged.
var installedSDKRoot string

// InstalledSDKRoot returns the read-only directory this binary's extension SDKs are installed in, or "" when they stage under the config root.
func InstalledSDKRoot() string { return installedSDKRoot }

// SetInstalledSDKRootForTest sets the installed SDK root and returns the function that restores the previous one. Only tests call it: a
// program sets the root at link time.
func SetInstalledSDKRootForTest(dir string) (restore func()) {
	previous := installedSDKRoot
	installedSDKRoot = dir
	return func() { installedSDKRoot = previous }
}

// CheckInstalledSDK reports why lang's installed SDK cannot serve a build: its directory is missing, or its marker names an SDK other than
// the one this binary embeds. It returns nil when the SDKs stage under the config root instead, and when the installed SDK matches.
func CheckInstalledSDK(lang string) error {
	if installedSDKRoot == "" {
		return nil
	}
	dir, err := StagedSDKDir("", lang)
	if err != nil {
		return err
	}
	return checkInstalledDir(lang+" extension SDK", dir, func() (string, error) { return registeredHash(lang) })
}

// CheckInstalledDir reports why dir, an installed directory whose marker file is marker, does not hold the content whose identity is want.
// label names the content in the error.
func CheckInstalledDir(label, dir, marker, want string) error {
	data, err := os.ReadFile(filepath.Join(dir, marker))
	if err != nil {
		if os.IsNotExist(err) {
			if _, statErr := os.Stat(dir); statErr != nil {
				return fmt.Errorf("installed %s is missing at %s; reinstall pig, the installation is incomplete", label, dir)
			}
			return fmt.Errorf("installed %s at %s has no %s marker; reinstall pig, the installation is incomplete", label, dir, marker)
		}
		return fmt.Errorf("read installed %s marker: %w", label, err)
	}
	if got := strings.TrimSpace(string(data)); got != want {
		return fmt.Errorf("installed %s at %s is %s but this pig embeds %s; reinstall pig, its installed files belong to another build", label, dir, got, want)
	}
	return nil
}

func checkInstalledDir(label, dir string, want func() (string, error)) error {
	hash, err := want()
	if err != nil {
		return err
	}
	return CheckInstalledDir(label, dir, MarkerFile, hash)
}

// registeredHash returns the content hash of the SDK registered for lang.
func registeredHash(lang string) (string, error) {
	sdkMu.RLock()
	sdk, ok := sdks[lang]
	sdkMu.RUnlock()
	if !ok {
		return "", unregistered(lang)
	}
	return sdk.hash()
}
