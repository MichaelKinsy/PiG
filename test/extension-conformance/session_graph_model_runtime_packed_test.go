package extensionconformance

import "testing"

// The model_runtime row (with the pending-write settle probe) in the packed cells of the Go, Rust, Python and Lisp SDKs.
func TestPackedSessionGraphModelRuntimeAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachSessionGraphPackedWith(t, newSessionGraphRig, func(t *testing.T, r *sessionGraphRig, _ string) {
		report, root := r.script("model_runtime")
		requireSessionGraphReport(t, normalizeSessionGraph(t, report, root), sessionGraphModelRuntimeWant)
	})
}
