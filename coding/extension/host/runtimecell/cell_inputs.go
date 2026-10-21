package runtimecell

import (
	"fmt"
	"strings"
)

// cellInputsUnchanged runs after a cell's compiler returned and before the cell is published. The cell's digest names its inputs as
// they were read before the build; the compiler read them later, after the wait for a peer's build lock and through its own
// compile. An input edited in that time is compiled into an artifact that its digest does not describe, and the cache would serve
// that artifact under the digest for good: the author puts the original input back and gets the build of the edit.
//
// hash is the digest the build was addressed by and rehashed the digest of the inputs now. verify holds the checks of the extension
// sources, whose content the caller hashed into the digest and the cell cannot read itself.
func cellInputsUnchanged(key, hash, rehashed string, verify []func() error) error {
	if after, failed := strings.CutPrefix(rehashed, "error:"); failed {
		return fmt.Errorf("hash cell %s after its build: %s", key, after)
	}
	if rehashed != hash {
		return fmt.Errorf("the inputs of cell %s changed while it was being built, so nothing was cached; start again to build the current inputs", key)
	}
	for _, check := range verify {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func goExtensionVerifiers(extensions []GoExtension) []func() error {
	var checks []func() error
	for _, extension := range extensions {
		if extension.Verify != nil {
			checks = append(checks, extension.Verify)
		}
	}
	return checks
}

func rustExtensionVerifiers(extensions []RustExtension) []func() error {
	var checks []func() error
	for _, extension := range extensions {
		if extension.Verify != nil {
			checks = append(checks, extension.Verify)
		}
	}
	return checks
}
