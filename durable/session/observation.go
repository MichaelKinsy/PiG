package session

import "github.com/MichaelKinsy/PiG/chord"

// NewSessionWatch returns a Chord Watch whose deliveries run on the Session's delivery scheduler and whose listener
// failures go to the Session's report, as Pi's Durable builds each Watch with (error) => session.report(error)
// (session.ts #attachDocument, view.ts, task-graph.ts, events.ts). detach runs once when the watch ends; replace gives the
// value an overflow delivers, and nil delivers the newest value.
func NewSessionWatch[T any](session *SessionImpl, value T, detach func(), replace func() T) *chord.Watch[T] {
	return chord.NewWatch(value, detach, session.reportListener, replace).On(session.scheduler)
}
