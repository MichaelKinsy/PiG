// Package tui is code mode's TUI facet: its card in the transcript (card.go), with the script's calls as nested.go reads them.
//
// pi: apps/p2/plugins/codemode/{nested,card,tui}.ts
package tui

import (
	"fmt"
	"regexp"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

// settled is a settled call's status in `details.calls`: a call still running when the script ended was cut short with it.
var settled = map[string]transcript.ToolStatus{
	"ok": transcript.ToolDone, "error": transcript.ToolError, "cancelled": transcript.ToolInterrupted, "running": transcript.ToolInterrupted,
}

// SettledCalls is the final list of a script's result `details`: `{ calls: [{ id, name, args (cut JSON), status: "ok" | "error" | "cancelled", durationMs?,
// error? }] }`. Only the script's own calls are listed, so every row is at depth 0. found is false without a list; ok is false for a malformed one.
func SettledCalls(callID string, details any) (calls []transcript.NestedCall, found, ok bool) {
	object, isObject := details.(*jsvalue.Object)
	if !isObject {
		return nil, false, false
	}
	list, isList := object.Lookup("calls").([]any)
	if !isList {
		return nil, false, false
	}
	calls = []transcript.NestedCall{}
	for _, item := range list {
		row, isRow := item.(*jsvalue.Object)
		if !isRow {
			return nil, true, false
		}
		id, idOK := row.Lookup("id").(float64)
		name, nameOK := row.Lookup("name").(string)
		args, argsOK := row.Lookup("args").(string)
		if !idOK || !nameOK || !argsOK {
			return nil, true, false
		}
		status, statusOK := row.Lookup("status").(string)
		if _, known := settled[status]; !statusOK || !known {
			return nil, true, false
		}
		call := transcript.NestedCall{ID: fmt.Sprintf("%s/c%v", callID, id), Name: name, ArgsText: args, Depth: 0}
		if parsed, err := jsvalue.Parse([]byte(args)); err == nil {
			if object, isObject := parsed.(*jsvalue.Object); isObject {
				call.Args, call.ArgsText = object, ""
			}
		}
		errText, hasError := row.Lookup("error").(string)
		if status == "error" && p2runtime.IsInterruptedError(row.Lookup("error")) {
			call.Status = transcript.ToolInterrupted
		} else {
			call.Status = settled[status]
		}
		if duration, isNumber := row.Lookup("durationMs").(float64); isNumber {
			call.DurationMs = &duration
		}
		if hasError {
			call.Error = errText
		}
		calls = append(calls, call)
	}
	return calls, true, true
}

// internal is code mode's internal tools' prefix (`pi.codemode.models.classify`): scripts call them by the rest.
var internal = regexp.MustCompile(`^pi\.codemode\.`)

// ScriptCalls is a script call's own calls: the settled list from its result's details (none when it is malformed, rather than wrong rows), else the live (or last
// seen) ones with internal tools by their script name, as the settled list has them. Nil is `undefined`.
func ScriptCalls(tool transcript.ToolView) []transcript.NestedCall {
	if calls, found, ok := SettledCalls(tool.ID, tool.Details); found {
		if !ok {
			return nil
		}
		return calls
	}
	live := tool.Calls
	hasInternal := false
	for _, call := range live {
		hasInternal = hasInternal || internal.MatchString(call.Name)
	}
	if !hasInternal {
		return live
	}
	renamed := make([]transcript.NestedCall, len(live))
	for i, call := range live {
		call.Name = strings.TrimPrefix(call.Name, "pi.codemode.")
		renamed[i] = call
	}
	return renamed
}

// RepeatedRuns is how an entry of a script's calls that groups count identical calls in a row says so.
func RepeatedRuns(count, failed int) string {
	text := fmt.Sprintf("%d runs", count)
	if failed > 0 {
		text += fmt.Sprintf(", %d failed", failed)
	}
	return text
}
