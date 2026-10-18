// Ports the end of packages/durable/test/harness-reports.test.ts:901-905 (Pi 482c1381a0): the error that fails the Session is
// reported as session_failed. It needs a Storage whose next commit fails, which the core's own store cannot give; the other
// expectations of that case run over both engines in durable/core/driver (harness_reports_core_test.go).

package harness

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

func TestAFailedSessionIsReportedAsSessionFailed(t *testing.T) {
	store := newControlledStorage()
	harness, _, reports := openTasks(t, store, nil)
	root := mustRoot(t, harness, nil)
	failure := errors.New("disk gone")
	store.failNextCommit(failure)
	_, _ = root.Commit(testContext, func(tx durable.Tx) (any, error) {
		return tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
	})
	<-harness.Closed()
	// harness-reports.test.ts:905: expect(infoOf("disk gone")).toEqual({ type: "session_failed" })
	for index, report := range reports.all() {
		if report.Error() == "disk gone" {
			if info := reports.allInfos()[index]; info != (durable.ReportInfo{Type: durable.ReportSessionFailed}) {
				t.Fatalf("report %+v, want {type: session_failed}", info)
			}
			return
		}
	}
	t.Fatalf("reports %v, want disk gone", reports.all())
}
