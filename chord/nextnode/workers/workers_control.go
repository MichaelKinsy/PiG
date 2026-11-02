package workers

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/next-node/workers/control.ts. The control channel between the server and a worker (slice 10 §3):
// JSON messages beside the link's frames on one IPC channel (the IPC link skips them), and errors as they cross it
// (`{ message, code?, reason? }`, never a stack or a cause).
//
// Forced Go differences. Upstream's `ToWorker` and `FromWorker` are discriminated unions of object literals; here each is
// one struct tagged by Kind whose fields are those of the kinds that carry them, and `controlOf` is generic over the two.
// A control message is JSON text on the channel (workers_channel.go), so each type writes and reads its own JSON in
// upstream's key order. `unknown` errors are `any`.

// CrossedError is an error as it crosses: its message, and a ChordError's code and reason.
type CrossedError struct {
	Message string
	Code    string
	Reason  string
}

func (e CrossedError) json() *delta.JsonObject {
	object := delta.JsonObjectOf("message", e.Message)
	if e.Code != "" {
		object.Set("code", e.Code)
	}
	if e.Reason != "" {
		object.Set("reason", e.Reason)
	}
	return object
}

func crossedErrorFromJSON(value any) (CrossedError, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return CrossedError{}, errors.New("workers: crossed error is not an object")
	}
	message, ok := object.Value("message").(string)
	if !ok {
		return CrossedError{}, errors.New("workers: crossed error has no message")
	}
	code, _ := object.Value("code").(string)
	reason, _ := object.Value("reason").(string)
	return CrossedError{Message: message, Code: code, Reason: reason}, nil
}

// ReloadedPlugins is the plugins of a reload report: names added, changed and removed.
type ReloadedPlugins struct {
	Added   []string
	Changed []string
	Removed []string
}

// CrossedFailure is a plugin that failed to load, its error crossed.
type CrossedFailure struct {
	Plugin string
	Error  CrossedError
}

// CrossedCause is a failure cause as it crosses; Kind is the cause's kind.
type CrossedCause struct {
	Kind  string
	Error CrossedError
}

// CrossedObject is an object of a reload report as it crosses; Cause is set only for a parked one.
type CrossedObject struct {
	Object string
	Kind   string
	Cause  *CrossedCause
}

// CrossedReport is a reload report as it crosses: its errors crossed.
type CrossedReport struct {
	Plugins ReloadedPlugins
	Failed  []CrossedFailure
	Objects []CrossedObject
}

func stringsJSON(values []string) []any {
	items := make([]any, len(values))
	for index, value := range values {
		items[index] = value
	}
	return items
}

func (r CrossedReport) json() *delta.JsonObject {
	failed := make([]any, len(r.Failed))
	for index, each := range r.Failed {
		failed[index] = delta.JsonObjectOf("plugin", each.Plugin, "error", each.Error.json())
	}
	objects := make([]any, len(r.Objects))
	for index, each := range r.Objects {
		object := delta.JsonObjectOf("object", each.Object, "kind", each.Kind)
		if each.Cause != nil {
			object.Set("cause", delta.JsonObjectOf("kind", each.Cause.Kind, "error", each.Cause.Error.json()))
		}
		objects[index] = object
	}
	return delta.JsonObjectOf(
		"plugins", delta.JsonObjectOf("added", stringsJSON(r.Plugins.Added), "changed", stringsJSON(r.Plugins.Changed), "removed", stringsJSON(r.Plugins.Removed)),
		"failed", failed,
		"objects", objects,
	)
}

func stringsFromJSON(value any) []string {
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		values = append(values, text)
	}
	return values
}

func crossedReportFromJSON(value any) (*CrossedReport, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, errors.New("workers: crossed report is not an object")
	}
	report := &CrossedReport{}
	if plugins, ok := object.Value("plugins").(*delta.JsonObject); ok {
		report.Plugins = ReloadedPlugins{
			Added:   stringsFromJSON(plugins.Value("added")),
			Changed: stringsFromJSON(plugins.Value("changed")),
			Removed: stringsFromJSON(plugins.Value("removed")),
		}
	}
	failed, _ := object.Value("failed").([]any)
	for _, item := range failed {
		entry, ok := item.(*delta.JsonObject)
		if !ok {
			return nil, errors.New("workers: crossed report failure is not an object")
		}
		plugin, _ := entry.Value("plugin").(string)
		crossedError, err := crossedErrorFromJSON(entry.Value("error"))
		if err != nil {
			return nil, err
		}
		report.Failed = append(report.Failed, CrossedFailure{Plugin: plugin, Error: crossedError})
	}
	objects, _ := object.Value("objects").([]any)
	for _, item := range objects {
		entry, ok := item.(*delta.JsonObject)
		if !ok {
			return nil, errors.New("workers: crossed report object is not an object")
		}
		name, _ := entry.Value("object").(string)
		kind, _ := entry.Value("kind").(string)
		crossedObject := CrossedObject{Object: name, Kind: kind}
		if cause, ok := entry.Value("cause").(*delta.JsonObject); ok {
			causeKind, _ := cause.Value("kind").(string)
			crossedError, err := crossedErrorFromJSON(cause.Value("error"))
			if err != nil {
				return nil, err
			}
			crossedObject.Cause = &CrossedCause{Kind: causeKind, Error: crossedError}
		}
		report.Objects = append(report.Objects, crossedObject)
	}
	return report, nil
}

// ToWorkerKind tags a ToWorker.
type ToWorkerKind string

const (
	ToWorkerStart    ToWorkerKind = "start"
	ToWorkerShutdown ToWorkerKind = "shutdown"
	ToWorkerReload   ToWorkerKind = "reload"
)

// FromWorkerKind tags a FromWorker.
type FromWorkerKind string

const (
	FromWorkerStarted  FromWorkerKind = "started"
	FromWorkerReady    FromWorkerKind = "ready"
	FromWorkerIdle     FromWorkerKind = "idle"
	FromWorkerFatal    FromWorkerKind = "fatal"
	FromWorkerReloaded FromWorkerKind = "reloaded"
)

// envelopeKey is the envelope's key: a control message is `{ "chord.workers": message }`.
const envelopeKey = "chord.workers"

// ctlMessage is a ToWorker or a FromWorker: what an envelope carries.
type ctlMessage interface {
	ToWorker | FromWorker
	json() *delta.JsonObject
	fromJSON(object *delta.JsonObject) (any, error)
}

// envelope is message in its envelope, as the JSON text that goes over the IPC channel.
func envelope[M ctlMessage](message M) ([]byte, error) {
	return json.Marshal(delta.JsonObjectOf(envelopeKey, message.json()))
}

// controlOf is the control message in an IPC message, or false for anything else (a link frame, the app's own). A trust
// boundary in name only: both ends are the app's processes, so only the envelope is checked. message is what the channel
// delivered: `[]byte` is a link's frame; a control message is its `json.RawMessage`.
func controlOf[M ctlMessage](message any) (M, bool) {
	var zero M
	raw, ok := message.(json.RawMessage)
	if !ok {
		return zero, false
	}
	value, err := delta.DecodeJson(raw)
	if err != nil {
		return zero, false
	}
	object, ok := value.(*delta.JsonObject)
	if !ok || !object.Has(envelopeKey) {
		return zero, false
	}
	inner, ok := object.Value(envelopeKey).(*delta.JsonObject)
	if !ok {
		return zero, false
	}
	decoded, err := zero.fromJSON(inner)
	if err != nil {
		return zero, false
	}
	return decoded.(M), true
}

// crossed is err as it crosses.
func crossed(err any) CrossedError {
	if chordErr, ok := err.(error); ok {
		if chord, ok := data.IsChordError(chordErr); ok {
			return CrossedError{Message: chord.Message, Code: string(chord.Code), Reason: string(chord.Reason)}
		}
		return CrossedError{Message: chordErr.Error()}
	}
	return CrossedError{Message: fmt.Sprint(err)}
}

// uncrossed is a crossed error as an error again: a ChordError when its code is one, an `error` otherwise.
func uncrossed(err CrossedError) error {
	if err.Code == "" {
		return errors.New(err.Message)
	}
	object := map[string]any{"code": err.Code, "message": err.Message}
	if err.Reason != "" {
		object["reason"] = err.Reason
	}
	chord, convertErr := data.ChordErrorFromJson(object)
	if convertErr != nil {
		return errors.New(err.Message)
	}
	return chord
}

// ToWorker is what the server sends a worker: Address, Start (JSON), Scripts and Lock only with ToWorkerStart; Scripts
// also with ToWorkerReload.
type ToWorker struct {
	Kind    ToWorkerKind
	Address contracts.Address
	Start   delta.JsonValue
	Scripts []plugins.PluginScript
	Lock    string
}

// FromWorker is what a worker sends the server: Idle with FromWorkerReady and FromWorkerIdle, Report with
// FromWorkerReady and a FromWorkerReloaded that applied, Error with FromWorkerFatal and a FromWorkerReloaded that failed.
type FromWorker struct {
	Kind   FromWorkerKind
	Idle   bool
	Report *CrossedReport
	Error  *CrossedError
}

func (m ToWorker) json() *delta.JsonObject {
	object := delta.JsonObjectOf("kind", string(m.Kind))
	switch m.Kind {
	case ToWorkerStart:
		object.Set("address", delta.JsonObjectOf("kind", m.Address.Kind, "params", m.Address.Params, "key", m.Address.Key))
		object.Set("start", m.Start)
		object.Set("scripts", scriptsJSON(m.Scripts))
		object.Set("lock", m.Lock)
	case ToWorkerReload:
		object.Set("scripts", scriptsJSON(m.Scripts))
	}
	return object
}

func (ToWorker) fromJSON(object *delta.JsonObject) (any, error) {
	kind, _ := object.Value("kind").(string)
	message := ToWorker{Kind: ToWorkerKind(kind)}
	switch message.Kind {
	case ToWorkerStart:
		address, ok := object.Value("address").(*delta.JsonObject)
		if !ok {
			return nil, errors.New("workers: start has no address")
		}
		message.Address.Kind, _ = address.Value("kind").(string)
		message.Address.Key, _ = address.Value("key").(string)
		message.Address.Params, _ = address.Value("params").(*delta.JsonObject)
		message.Start = object.Value("start")
		message.Lock, _ = object.Value("lock").(string)
		scripts, err := scriptsFromJSON(object.Value("scripts"))
		if err != nil {
			return nil, err
		}
		message.Scripts = scripts
	case ToWorkerReload:
		scripts, err := scriptsFromJSON(object.Value("scripts"))
		if err != nil {
			return nil, err
		}
		message.Scripts = scripts
	case ToWorkerShutdown:
	default:
		return nil, fmt.Errorf("workers: unknown control message %q", kind)
	}
	return message, nil
}

func (m FromWorker) json() *delta.JsonObject {
	object := delta.JsonObjectOf("kind", string(m.Kind))
	switch m.Kind {
	case FromWorkerReady:
		object.Set("idle", m.Idle)
		object.Set("report", m.Report.json())
	case FromWorkerIdle:
		object.Set("idle", m.Idle)
	case FromWorkerFatal:
		object.Set("error", m.Error.json())
	case FromWorkerReloaded:
		if m.Report != nil {
			object.Set("report", m.Report.json())
		} else {
			object.Set("error", m.Error.json())
		}
	}
	return object
}

func (FromWorker) fromJSON(object *delta.JsonObject) (any, error) {
	kind, _ := object.Value("kind").(string)
	message := FromWorker{Kind: FromWorkerKind(kind)}
	switch message.Kind {
	case FromWorkerReady:
		message.Idle, _ = object.Value("idle").(bool)
		report, err := crossedReportFromJSON(object.Value("report"))
		if err != nil {
			return nil, err
		}
		message.Report = report
	case FromWorkerIdle:
		message.Idle, _ = object.Value("idle").(bool)
	case FromWorkerFatal:
		crossedError, err := crossedErrorFromJSON(object.Value("error"))
		if err != nil {
			return nil, err
		}
		message.Error = &crossedError
	case FromWorkerReloaded:
		if object.Has("report") {
			report, err := crossedReportFromJSON(object.Value("report"))
			if err != nil {
				return nil, err
			}
			message.Report = report
		} else {
			crossedError, err := crossedErrorFromJSON(object.Value("error"))
			if err != nil {
				return nil, err
			}
			message.Error = &crossedError
		}
	case FromWorkerStarted:
	default:
		return nil, fmt.Errorf("workers: unknown control message %q", kind)
	}
	return message, nil
}

// scriptsJSON is scripts as the JSON they cross as: plain data.
func scriptsJSON(scripts []plugins.PluginScript) any {
	if scripts == nil {
		scripts = []plugins.PluginScript{}
	}
	text, err := json.Marshal(scripts) //perf:ok once per worker start or reload message (a process lifecycle event, not per request)
	if err != nil {
		return []any{}
	}
	value, err := delta.DecodeJson(text) //perf:ok once per worker start or reload message (a process lifecycle event, not per request)
	if err != nil {
		return []any{}
	}
	return value
}

// scriptsFromJSON is the scripts that crossed as value.
func scriptsFromJSON(value any) ([]plugins.PluginScript, error) {
	text, err := chordjson.Marshal(value) //perf:ok once per worker start or reload message (a process lifecycle event, not per request)
	if err != nil {
		return nil, err
	}
	var scripts []plugins.PluginScript
	if err := json.Unmarshal(text, &scripts); err != nil { //perf:ok once per worker start or reload message (a process lifecycle event, not per request)
		return nil, err
	}
	return scripts, nil
}

// crossedFailures is failed with their errors crossed.
func crossedFailures(failed []host.PluginFailure) []CrossedFailure {
	crossedList := make([]CrossedFailure, len(failed))
	for index, each := range failed {
		crossedList[index] = CrossedFailure{Plugin: each.Plugin, Error: crossed(each.Error)}
	}
	return crossedList
}

// crossedReport is report as it crosses.
func crossedReport(report host.ReloadReport) CrossedReport {
	objects := make([]CrossedObject, len(report.Objects))
	for index, each := range report.Objects {
		objects[index] = CrossedObject{Object: each.Object, Kind: string(each.Kind)}
		if each.Kind == host.ReloadParked {
			objects[index].Cause = &CrossedCause{Kind: string(each.Cause.Kind), Error: crossed(each.Cause.Error)}
		}
	}
	return CrossedReport{
		Plugins: ReloadedPlugins{Added: report.Plugins.Added, Changed: report.Plugins.Changed, Removed: report.Plugins.Removed},
		Failed:  crossedFailures(report.Failed),
		Objects: objects,
	}
}

// uncrossedReport is a crossed report as a report again.
func uncrossedReport(report CrossedReport) host.ReloadReport {
	result := host.ReloadReport{Failed: make([]host.PluginFailure, len(report.Failed)), Objects: make([]host.ReloadedObject, len(report.Objects))}
	result.Plugins.Added, result.Plugins.Changed, result.Plugins.Removed = report.Plugins.Added, report.Plugins.Changed, report.Plugins.Removed
	for index, each := range report.Failed {
		result.Failed[index] = host.PluginFailure{Plugin: each.Plugin, Error: uncrossed(each.Error)}
	}
	for index, each := range report.Objects {
		result.Objects[index] = host.ReloadedObject{Object: each.Object, Kind: host.ReloadedObjectKind(each.Kind)}
		if each.Cause != nil {
			err := uncrossed(each.Cause.Error)
			// A parked object's cause is a ChordError on the worker; one that crossed without a code is wrapped as one.
			chord, ok := data.IsChordError(err)
			if !ok {
				chord = data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: err.Error()}, nil)
			}
			result.Objects[index].Cause = data.FailureCause{Kind: data.FailureKind(each.Cause.Kind), Error: chord}
		}
	}
	return result
}
