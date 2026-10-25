package pigsdklock

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/contentdirs"
)

type registeredSDK struct {
	base string
	hash func() (string, error)
}

var (
	sdkMu sync.RWMutex
	sdks  = map[string]registeredSDK{}
)

// RegisterSDK records the extension SDK this binary embeds for lang: it stages as <config-root>/state/pigsdk/<base>-<hash16>, hash16 being the first 16 hex digits of hash's result. A later registration of lang replaces the earlier one.
func RegisterSDK(lang, base string, hash func() (string, error)) {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	sdks[lang] = registeredSDK{base: base, hash: hash}
}

// StagedSDKDir returns the directory this binary reads lang's extension SDK from: the one it stages the SDK in under configRoot, or, when
// the SDKs are installed ([InstalledSDKRoot]), the installed directory of the same name. It fails when no SDK is registered for lang and when
// the SDK's content hash cannot be computed. Package coding/extension/pigsdk registers the SDKs and every program that links package coding
// links it, so an unregistered language means a program built on the extension host alone; the error names the import that fixes it.
func StagedSDKDir(configRoot, lang string) (string, error) {
	sdkMu.RLock()
	sdk, ok := sdks[lang]
	sdkMu.RUnlock()
	if !ok {
		return "", unregistered(lang)
	}
	hash, err := sdk.hash()
	if err != nil {
		return "", err
	}
	name, err := contentdirs.Name(sdk.base, hash)
	if err != nil {
		return "", fmt.Errorf("%s extension SDK: %w", lang, err)
	}
	// pig additive (D195): an installed SDK is read in place; nothing stages under the config root.
	if installedSDKRoot != "" {
		return filepath.Join(installedSDKRoot, name), nil
	}
	return filepath.Join(configRoot, "state", "pigsdk", name), nil
}

func unregistered(lang string) error {
	return fmt.Errorf("this program links no %s extension SDK; import github.com/MichaelKinsy/PiG/coding/extension/pigsdk to embed and stage the SDKs", lang)
}
