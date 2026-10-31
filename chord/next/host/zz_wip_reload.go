package host

import "github.com/MichaelKinsy/PiG/chord/next/data"

// WIP: stand-in for KReload's report types (host_host.go), dropped when the real ones are merged.

// PluginFailure is a plugin that failed.
type PluginFailure struct {
	Plugin string
	Error  error
}

// LoadReport is what Load reports.
type LoadReport struct{ Failed []PluginFailure }

// PluginChanges is the plugins a reload added, changed and removed.
type PluginChanges struct{ Added, Changed, Removed []string }

// ReloadedObjectKind is what a reload did to an object.
type ReloadedObjectKind string

const (
	ReloadSwapped   ReloadedObjectKind = "swapped"
	ReloadRestarted ReloadedObjectKind = "restarted"
	ReloadEnded     ReloadedObjectKind = "ended"
	ReloadParked    ReloadedObjectKind = "parked"
	ReloadRetried   ReloadedObjectKind = "retried"
)

// ReloadedObject is one object of a reload report; Cause is set for a parked one.
type ReloadedObject struct {
	Object string
	Kind   ReloadedObjectKind
	Cause  data.FailureCause
}

// ReloadReport is what a reload reports.
type ReloadReport struct {
	Plugins PluginChanges
	Failed  []PluginFailure
	Objects []ReloadedObject
}
