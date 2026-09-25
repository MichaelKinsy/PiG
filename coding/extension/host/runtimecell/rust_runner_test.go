package runtimecell

import (
	"strings"
	"testing"
)

func TestRustPackedRunnerUsesSDKTransportForLoadFailure(t *testing.T) {
	// Upstream loader.ts awaits each factory and surfaces its failure. A packed member must wake that same host wait using the SDK's platform transport.
	source := renderRustRunner([]RustExtension{{
		Name: "failing-member", Crate: "failing_member", Factory: "new_extension",
	}})
	if strings.Contains(source, "std::os::unix") {
		t.Fatal("generated runner bypasses the SDK transport with a Unix-only API")
	}
	if !strings.Contains(source, "pig_sdk::report_load_failure(&sock)") {
		t.Fatal("generated runner does not report the failed member through its SDK socket")
	}
}
