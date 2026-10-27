package extensionconformance

import "testing"

// TestSessionGraphLispPackedAcrossSDKs runs the SessionManager, SettingsManager, ProjectTrustStore, DefaultResourceLoader and DefaultPackageManager scripts in the Lisp SDK's packed cell (shared with a second Lisp extension): a packed cell must report what Pi's own classes report, as the isolated cell does.
func TestSessionGraphLispPackedAcrossSDKs(t *testing.T) {
	t.Parallel()
	lisp := map[string]sessionGraphLoader{"lisp-packed": sessionGraphPackedSDKs()["lisp-packed"]}
	for _, row := range []struct{ tool, want string }{
		{"session_manager", sessionGraphSessionManagerWant},
		{"settings_manager", sessionGraphSettingsManagerWant},
		{"project_trust_store", sessionGraphProjectTrustStoreWant},
		{"resource_loader", sessionGraphDefaultResourceLoaderWant},
		{"package_manager", sessionGraphDefaultPackageManagerWant},
	} {
		t.Run(row.tool, func(t *testing.T) {
			eachSessionGraphCellWith(t, newSessionGraphRig, lisp, func(t *testing.T, r *sessionGraphRig, _ string) {
				report, root := r.script(row.tool)
				requireSessionGraphReport(t, normalizeSessionGraph(t, report, root), row.want)
			})
		})
	}
}

// lispPackedHarnessCase is the Lisp SDK harness fixture beside a peer extension in one shared cell.
func lispPackedHarnessCase() harnessCase {
	return harnessCase{name: "subprocess-lisp-packed", extName: lispFixtureName, make: func(t *testing.T) *harness { return makePackedUIHarness(t, "lisp") }}
}
