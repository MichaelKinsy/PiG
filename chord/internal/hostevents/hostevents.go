// Package hostevents is where a Go host's event flush (chord/next/host) and the plugins bridge (chord/nextnode/plugins)
// meet. No Pi file corresponds to it: in Pi the app's facets and the plugins' load into one host, so an event of an app
// object reaches a plugin's listener inside that host's flush. In PiG the plugins' host is a Node subprocess and the
// app's facets are on a Go host, so the bridge carries the Go host's events to the Node host as a link carries them
// (Pi remote/provider.ts `queueEvent`, `sendEvents`, `#eventAck`): one message per flush, in entry order, under the
// host's event window, acknowledged by count.
//
// The package is internal so that nothing is added to chord/next/host's API: the host sets Open when it is linked in,
// and only code under chord/ can call it.
package hostevents

// Entry is one event of a flush's message, as a link's `event` message carries one (link.WireEvent): the hold its
// reference was tapped under, its member (an index into the reference's event members sorted by name), the emitting
// host's stamp (unix milliseconds) and its value as the link's JSON part text (link.JsonPart). Its JSON form is the
// bridge's entry of an `event` message, in both directions.
type Entry struct {
	Hold   int    `json:"hold"`
	Member int    `json:"member"`
	At     int64  `json:"at"`
	Value  string `json:"value"`
}

// Port takes the event flushes of one Go host for the references tapped on it: per flush, the entries of every tapped
// reference in the host's entry order, handed to the port's send as one message while fewer than the host's event
// window (HostOptions.EventWindow, 32 by default) are unacknowledged. A flush that finds the window full drops its
// entries for this port as it visits them, before anything is encoded, counted `chord.events.dropped{reason=window}` on
// the host's telemetry: nothing waits, nothing is queued, nothing fails.
type Port interface {
	// Tap makes the port get what reference (a *host.Reference) receives on its event members from now on, under hold.
	// It returns the members' names in the order Entry.Member indexes. It fails for a released reference, for one
	// whose contract has no event member, for one of another host, for a hold in use and for a closed port.
	Tap(reference any, hold int) (members []string, err error)
	// Untap ends hold's tap (nothing for an unknown hold).
	Untap(hold int)
	// Ack tells that the consumer consumed that many more messages: each frees one slot of the window. One that counts
	// more than is unacknowledged frees only what is: the window never grows. It never blocks: the slots are free
	// from the host's next turn on.
	Ack(messages int)
	// Close ends every tap; a closed port sends nothing more.
	Close()
}

// Open opens a Port on the host of reference (a *host.Reference whose contract has event members, not released). send
// gets each flush's message inside the host's section, before the flush calls its listeners: it must not block and
// must not call into the host. chord/next/host sets Open.
var Open func(reference any, send func(entries []Entry)) (Port, error)
