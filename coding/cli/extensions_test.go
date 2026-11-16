// pig-specific: no upstream equivalent.

package cli

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestLoadSubprocessExtensions_NoConfigsReturnsNil(t *testing.T) {
	t.Parallel()

	exts, host, bridge, errs := loadSubprocessExtensions(context.Background(), t.TempDir(), extension.ModePrint, nil, nil, nil, nil)
	if exts != nil || host != nil || bridge != nil || errs != nil {
		t.Fatalf("loadSubprocessExtensions() = (%v, %v, %v, %v), want all nil", exts, host, bridge, errs)
	}
}
