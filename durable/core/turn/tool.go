// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// ToolDef is the pi.tool task definition (tool.ts ToolTask, version 2): input {kind: "model", assistant, callId}, first
// checkpoint {phase: "call"}. Version 1 had only model-issued calls and no kind (migrateToolTask).
// Read-only: assigned once at initialization.
var ToolDef = &session.TaskDef{
	Name:    KindTool,
	Version: 2,
	Initial: func(any) any {
		o := jv.NewObject()
		o.Set("phase", "call")
		return o
	},
}

// Entry kinds of the built-in entries (entries.ts).
const (
	KindUser       = "pi.user"
	KindAssistant  = "pi.assistant"
	KindToolResult = "pi.tool-result"
	KindReset      = "pi.reset"
)

// entryDraft is {kind, ...fields}: the draft Tx.AppendEntry stores for a typed entry.
func entryDraft(kind string, fields *jv.Object) *jv.Object {
	// A typed appendEntry spreads the draft, then sets kind ({...value, kind}, session/transaction.ts appendEntry).
	d := jv.NewObject()
	fields.Entries(func(k string, v any) bool {
		d.Set(k, v)
		return true
	})
	d.Set("kind", kind)
	return d
}

func diagnostic(severity, code, message string) *jv.Object {
	d := jv.NewObject()
	d.Set("severity", severity)
	d.Set("code", code)
	d.Set("message", message)
	return d
}

// HarnessError is harnessError: an error result the Harness writes itself, with no content and one error diagnostic.
func HarnessError(code, message string) *jv.Object {
	r := jv.NewObject()
	r.Set("output", []any{})
	r.Set("isError", true)
	r.Set("diagnostics", []any{diagnostic("error", code, message)})
	return r
}

// renderDiagnostics is the text appended to a tool result's content: <harness>\n[severity] message…\n</harness>.
func renderDiagnostics(diagnostics []any) string {
	var out strings.Builder
	out.WriteString("<harness>\n")
	for i, d := range diagnostics {
		o, _ := d.(*jv.Object)
		severity, _ := stringField(o, "severity")
		message, _ := stringField(o, "message")
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString("[" + severity + "] " + message)
	}
	return out.String() + "\n</harness>"
}

// AppendToolResult is appendToolResult: it appends a pi.tool-result entry whose content ends with the rendered
// diagnostics, so the stored message is what the model sees, and adds the result's usage to pi.usage in the same commit.
// durationMs is nil when unknown. call is the tool call content block ({id, name, …}).
func AppendToolResult(tx *session.Tx, conversation ID, call, result *jv.Object, timestamp float64, durationMs *float64) (*jv.Object, error) {
	diagnostics, _ := arrayField(result, "diagnostics")
	content, _ := arrayField(result, "output")
	content = append([]any{}, content...)
	if len(diagnostics) > 0 {
		text := jv.NewObject()
		text.Set("type", "text")
		text.Set("text", renderDiagnostics(diagnostics))
		content = append(content, text)
	}
	id, _ := call.Get("id")
	name, _ := call.Get("name")
	message := jv.NewObject()
	message.Set("role", "toolResult")
	message.Set("toolCallId", id)
	message.Set("toolName", name)
	message.Set("content", content)
	if details, ok := result.Get("details"); ok {
		message.Set("details", details)
	}
	usage, hasUsage := result.Get("usage")
	if hasUsage {
		message.Set("usage", usage)
	}
	isError, _ := result.Get("isError")
	message.Set("isError", isError == true)
	if durationMs != nil {
		message.Set("durationMs", *durationMs)
	}
	message.Set("timestamp", timestamp)
	if hasUsage {
		if u, ok := usage.(*jv.Object); ok {
			if err := recordUsageTx(tx, conversation, UsageTools, jsString(name), u); err != nil {
				return nil, err
			}
		}
	}
	data := jv.NewObject()
	data.Set("diagnostics", append([]any{}, diagnostics...))
	fields := jv.NewObject()
	fields.Set("model", []any{message})
	fields.Set("data", data)
	return tx.AppendEntry(conversation, entryDraft(KindToolResult, fields))
}

func jsString(v any) string {
	s, _ := v.(string)
	return s
}

func arrayField(o *jv.Object, key string) ([]any, bool) {
	v, ok := o.Get(key)
	a, isArray := v.([]any)
	return a, ok && isArray
}

// recordUsageTx adds usage to the conversation's pi.usage document in this commit.
func recordUsageTx(tx *session.Tx, conversation ID, bucket, key string, usage *jv.Object) error {
	doc, err := tx.Doc(UsageDef, session.DocArgs{Owner: conversation})
	if err != nil {
		return err
	}
	totals := doc.Obj(bucket)
	if totals == nil {
		return nil
	}
	if current := totals.Obj(key); current != nil {
		return addUsageDraft(current, usage)
	}
	return totals.Set(key, jv.Clone(usage))
}

// migrateToolTask is the pi.tool migration from version 1 (tool.ts): `{ kind: "model", ...input }`, the checkpoint unchanged.
func migrateToolTask(input, checkpoint any, _ int) (any, any, error) {
	old, ok := input.(*jv.Object)
	if !ok {
		return input, checkpoint, nil
	}
	migrated := jobj("kind", "model")
	for _, key := range old.Keys() {
		value, _ := old.Get(key)
		migrated.Set(key, value)
	}
	return migrated, checkpoint, nil
}
