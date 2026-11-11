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

// A failing member's line (`extension "a" load failed: cause`) and the panic output of the next member share one log, and the panic hook writes in
// pieces: the next member must not start until the failing member's line is written and the host's wait for it has ended, as the Go runner's deferred
// built() holds it. Otherwise the pieces interleave and the host, finding no intact line for "a", reads the cause of the member that panicked last.
func TestRustPackedRunnerHoldsTheNextMemberUntilAFailureIsReported(t *testing.T) {
	source := renderRustRunner([]RustExtension{{Name: "a", Crate: "a", Factory: "new_extension"}, {Name: "b", Crate: "b", Factory: "new_extension"}})
	report := strings.Index(source, "pig_sdk::report_load_failure(&sock)")
	release := strings.Index(source, "drop(reported)")
	if report < 0 || release < report {
		t.Fatalf("the failing member's thread releases the next member (drop(reported) at %d) before it reports its failure (at %d)", release, report)
	}
	if !strings.Contains(source, "let reported = built_sender.clone();") {
		t.Fatal("the failing member's thread does not keep the built channel open until it reports")
	}
}
