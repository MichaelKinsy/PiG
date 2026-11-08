package surfacehost_test

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
)

// watchSessions follows the `sessions` member of a held Sessions reference.
func watchSessions(scope *tui.TuiScope, ref *host.Reference) func() ([]any, bool) {
	read := tui.WatchMember(scope, ref.Member("sessions"))
	return func() ([]any, bool) {
		value, ok := read()
		if !ok {
			return nil, false
		}
		rows, _ := value.([]any)
		return rows, rows != nil || value != nil
	}
}

var _ = delta.NewJsonObject
