package sessiontest

import (
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// Elapsed is the Loop.Elapsed of a Session over a store of the given profile whose reads the Session's own transaction charges cover: Pi's ticks
// from a line job's start to the return of its work (hopcost.Job.Return, hopcost.ReadReturn).
func Elapsed(p hopcost.Profile) func(session.Ended) (callback, work int) {
	return func(e session.Ended) (int, int) {
		change := max(e.Ticks.Change, 0)
		if e.Read {
			return 0, hopcost.ReadReturn(change, false)
		}
		if e.Err != nil {
			if e.Ticks.Change < 0 {
				// It failed before its callback ran (the Session failed, or the caller's signal was aborted): the work rejects at once.
				return 0, 0
			}
			return 0, hopcost.Job{Change: change}.ReturnFailed()
		}
		j := hopcost.Job{Change: change, Settle: hopcost.Settle(e.Ticks.SettleOwners, e.Ticks.SettleCached, e.Ticks.SettleReads)}
		return j.Callback(), j.Return() + e.Ticks.Membership
	}
}

// MemoryStoreTicks is the Loop.StoreTicks of MemoryStorage: it reads its ID counter and stores in the tick of the call, and the writer resumes
// hopcost.MemoryCommitResume ticks after it (`await storage.commit(...)` through the Session's guard).
func MemoryStoreTicks([][]session.Write) (read, receive, resume int) {
	return 0, 0, hopcost.MemoryCommitResume
}
