package subprocess

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// sweepUnit is the lone UTF-16 surrogate U+D83D as a Go string holds it.
const sweepUnit = "\xed\xa0\xbd"

// sweepFill writes a labelled text with the unit into every text member reflection reaches.
type sweepFill struct {
	n        int
	paths    map[string]string
	unfilled []string
	// clean fills the members with plain text instead (the benchmark of an ordinary frame).
	clean bool
}

func (f *sweepFill) label(path string) string {
	f.n++
	label := fmt.Sprintf("L%03d", f.n)
	f.paths[label] = path
	if f.clean {
		return label + "[ok]"
	}
	return label + "[" + sweepUnit + "]"
}

func (f *sweepFill) escaped(path string) string {
	return strings.Replace(f.label(path), sweepUnit, `\ud83d`, 1)
}

var (
	sweepMessageType = reflect.TypeFor[extension.AgentMessage]()
	sweepRawType     = reflect.TypeFor[json.RawMessage]()
	sweepStringType  = reflect.TypeFor[string]()
	sweepPromptType  = reflect.TypeFor[*extension.SystemPrompt]()
	sweepDraftType   = reflect.TypeFor[extension.SessionBoundaryDraft]()
	sweepEntryType   = reflect.TypeFor[sessionentry.SessionEntry]()
	// sweepConcrete are the values an interface-typed member is filled with: the first that implements the interface.
	sweepConcrete = []reflect.Type{
		reflect.TypeFor[ai.TextContent](), reflect.TypeFor[*ai.TextContent](), reflect.TypeFor[ai.UserContentBlocks](),
		reflect.TypeFor[ai.UserMessage](), reflect.TypeFor[*ai.UserMessage](), reflect.TypeFor[ai.TextDeltaEvent](), reflect.TypeFor[*ai.TextDeltaEvent](),
	}
	// sweepDrafts are the union members SessionBoundaryDraft.MarshalJSON writes differently; the empty type is its default branch.
	sweepDrafts = []extension.SessionBoundaryDraftType{extension.SessionBoundaryDraftCustom, extension.SessionBoundaryDraftCustomMessage,
		extension.SessionBoundaryDraftContextEdit, extension.SessionBoundaryDraftCompaction, ""}
)

// sweepEntryKinds is how many kinds of session entry entry builds.
const sweepEntryKinds = 6

// entry is a session entry of the given kind, decoded from the JSON a session file holds, with a labelled unit in each of its text members.
func (f *sweepFill) entry(path string, kind int) (reflect.Value, bool) {
	esc := func(member string) string { return f.escaped(fmt.Sprintf("%s{entry %d}.%s", path, kind, member)) }
	base := fmt.Sprintf(`"id":"e%d","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z"`, kind)
	var text string
	switch kind % sweepEntryKinds {
	case 0:
		text = `{"type":"message",` + base + `,"message":{"role":"user","content":"` + esc("message.content") + `","timestamp":1}}`
	case 1:
		text = `{"type":"custom",` + base + `,"customType":"` + esc("customType") + `","data":{"k":"` + esc("data.k") + `"}}`
	case 2:
		text = `{"type":"custom_message",` + base + `,"customType":"` + esc("customType") + `","content":"` + esc("content") + `","display":true,"details":{"d":"` + esc("details.d") + `"}}`
	case 3:
		text = `{"type":"compaction",` + base + `,"summary":"` + esc("summary") + `","firstKeptEntryId":"e0","tokensBefore":1}`
	case 4:
		text = `{"type":"branch_summary",` + base + `,"fromId":"e0","summary":"` + esc("summary") + `"}`
	default:
		text = `{"type":"label",` + base + `,"targetId":"e0","label":"` + esc("label") + `"}`
	}
	decoded := sessionentry.DecodeSessionEntry(json.RawMessage(text))
	if decoded == nil {
		f.unfilled = append(f.unfilled, fmt.Sprintf("%s: session entry %d did not decode", path, kind))
		return reflect.Value{}, false
	}
	return reflect.ValueOf(decoded), true
}

func (f *sweepFill) message(path string, kind int) (extension.AgentMessage, error) {
	var text string
	switch kind % 5 {
	case 0:
		text = `{"role":"user","content":"` + f.escaped(path+"{user}.content") + `","timestamp":1}`
	case 1:
		text = `{"role":"user","content":[{"type":"text","text":"` + f.escaped(path+"{user}.content[0].text") + `"}],"timestamp":1}`
	case 2:
		text = `{"role":"assistant","content":[{"type":"text","text":"` + f.escaped(path+"{assistant}.content.text") + `"},{"type":"thinking","thinking":"` + f.escaped(path+"{assistant}.content.thinking") +
			`"},{"type":"toolCall","id":"c1","name":"t","arguments":{"p":"` + f.escaped(path+"{assistant}.content.toolCall.arguments") + `"}}],"api":"anthropic-messages","provider":"p","model":"m",` +
			`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}`
	case 3:
		text = `{"role":"toolResult","toolCallId":"c1","toolName":"t","content":[{"type":"text","text":"` + f.escaped(path+"{toolResult}.content[0].text") + `"}],"isError":false,"timestamp":1}`
	default:
		text = `{"role":"custom","customType":"x","content":"` + f.escaped(path+"{custom}.content") + `","display":true,"timestamp":1}`
	}
	var message extension.AgentMessage
	err := jsstring.UnmarshalKeepingSurrogates([]byte(text), &message)
	return message, err
}

func (f *sweepFill) fill(v reflect.Value, path string, depth int) {
	if depth > 7 || !v.CanSet() {
		return
	}
	t := v.Type()
	switch t {
	case sweepMessageType:
		message, err := f.message(path, 2)
		if err != nil {
			f.unfilled = append(f.unfilled, path+": message decode: "+err.Error())
			return
		}
		v.Set(reflect.ValueOf(message))
		return
	case sweepRawType:
		v.SetBytes([]byte(`{"raw":"` + f.escaped(path+"(raw JSON)") + `"}`))
		return
	case sweepPromptType:
		text := f.label(path)
		v.Set(reflect.ValueOf(extension.NewSystemPrompt(func() (string, error) { return text, nil })))
		return
	case sweepEntryType:
		if entry, ok := f.entry(path, 2); ok {
			v.Set(entry)
		}
		return
	}
	switch v.Kind() {
	case reflect.String:
		if t == sweepStringType && !strings.HasSuffix(path, ".Type") {
			v.SetString(f.label(path))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			f.fill(v.Elem(), path, depth+1)
			return
		}
		switch t.Elem().Kind() {
		case reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		default:
			target := reflect.New(t.Elem())
			f.fill(target.Elem(), path, depth+1)
			v.Set(target)
		}
	case reflect.Struct:
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				f.fill(v.Field(i), path+"."+t.Field(i).Name, depth+1)
			}
		}
	case reflect.Slice:
		if t.Elem() == sweepMessageType {
			list := reflect.MakeSlice(t, 0, 5)
			for kind := range 5 {
				message, err := f.message(fmt.Sprintf("%s[%d]", path, kind), kind)
				if err != nil {
					f.unfilled = append(f.unfilled, fmt.Sprintf("%s[%d]: message decode: %v", path, kind, err))
					continue
				}
				list = reflect.Append(list, reflect.ValueOf(message))
			}
			v.Set(list)
			return
		}
		if t.Elem().Kind() == reflect.Uint8 {
			return
		}
		if t.Elem() == sweepEntryType {
			list := reflect.MakeSlice(t, 0, sweepEntryKinds)
			for kind := range sweepEntryKinds {
				if entry, ok := f.entry(fmt.Sprintf("%s[%d]", path, kind), kind); ok {
					list = reflect.Append(list, entry)
				}
			}
			v.Set(list)
			return
		}
		if t.Elem() == sweepDraftType {
			list := reflect.MakeSlice(t, len(sweepDrafts), len(sweepDrafts))
			for i, draft := range sweepDrafts {
				f.fill(list.Index(i), fmt.Sprintf("%s[%s]", path, draft), depth+1)
				list.Index(i).FieldByName("Type").SetString(string(draft))
			}
			v.Set(list)
			return
		}
		list := reflect.MakeSlice(t, 1, 1)
		f.fill(list.Index(0), path+"[0]", depth+1)
		v.Set(list)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return
		}
		entry := reflect.New(t.Elem()).Elem()
		f.fill(entry, path+"[k]", depth+1)
		filled := reflect.MakeMap(t)
		filled.SetMapIndex(reflect.ValueOf("k").Convert(t.Key()), entry)
		v.Set(filled)
	case reflect.Interface:
		if t.NumMethod() == 0 {
			v.Set(reflect.ValueOf(map[string]any{"type": "text", "text": f.label(path + "(any)")}))
			return
		}
		for _, concrete := range sweepConcrete {
			if concrete.Implements(t) {
				value := reflect.New(concrete).Elem()
				f.fill(value, fmt.Sprintf("%s{%s}", path, concrete), depth+1)
				v.Set(value)
				return
			}
		}
		f.unfilled = append(f.unfilled, path+" "+t.String())
	}
}

func sweepMarshal(event any) (frame []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return marshalEventWire(boundaryEventWire(wireEventPayload(event), nil))
}

// TestEventFrameSurrogateSweep pins how the host writes a lone UTF-16 surrogate to an extension: Pi hands a Node extension the event object itself, so
// a string of an event is the string Pi holds, and JSON.stringify writes a lone unit as its escape. For every extension event type the arguments the
// host encodes for the event request (marshalEventWire, the encoder of Host's event dispatch) spell the unit of each text member as that escape; a
// member written as three U+FFFD sits under a type whose own MarshalJSON encodes with a plain encoding/json Marshal.
func TestEventFrameSurrogateSweep(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeFor[extension.ProjectTrustEvent](),
		reflect.TypeFor[extension.ResourcesDiscoverEvent](),
		reflect.TypeFor[extension.McpServersChangeEvent](),
		reflect.TypeFor[extension.SessionStartEvent](),
		reflect.TypeFor[extension.SessionInfoChangedEvent](),
		reflect.TypeFor[extension.SessionBeforeSwitchEvent](),
		reflect.TypeFor[extension.SessionBeforeForkEvent](),
		reflect.TypeFor[extension.SessionBeforeCompactEvent](),
		reflect.TypeFor[extension.SessionCompactEvent](),
		reflect.TypeFor[extension.SessionCompactFailedEvent](),
		reflect.TypeFor[extension.SessionShutdownEvent](),
		reflect.TypeFor[extension.SessionBeforeTreeEvent](),
		reflect.TypeFor[extension.SessionTreeEvent](),
		reflect.TypeFor[extension.ContextEvent](),
		reflect.TypeFor[extension.ContextWithSystemEvent](),
		reflect.TypeFor[extension.CacheWarmingDecisionEvent](),
		reflect.TypeFor[extension.BeforeProviderRequestEvent](),
		reflect.TypeFor[extension.BeforeProviderHeadersEvent](),
		reflect.TypeFor[extension.AfterProviderResponseEvent](),
		reflect.TypeFor[extension.ProviderStreamEvent](),
		reflect.TypeFor[extension.BeforeAgentStartEvent](),
		reflect.TypeFor[extension.AgentStartEvent](),
		reflect.TypeFor[extension.AgentEndEvent](),
		reflect.TypeFor[extension.AgentBeforeSettleEvent](),
		reflect.TypeFor[extension.AgentSettledEvent](),
		reflect.TypeFor[extension.UIPromptStartEvent](),
		reflect.TypeFor[extension.UIPromptEndEvent](),
		reflect.TypeFor[extension.TurnStartEvent](),
		reflect.TypeFor[extension.TurnEndEvent](),
		reflect.TypeFor[extension.MessageStartEvent](),
		reflect.TypeFor[extension.MessageUpdateEvent](),
		reflect.TypeFor[extension.MessageEndEvent](),
		reflect.TypeFor[extension.ToolExecutionStartEvent](),
		reflect.TypeFor[extension.ToolExecutionUpdateEvent](),
		reflect.TypeFor[extension.ToolExecutionEndEvent](),
		reflect.TypeFor[extension.ModelSelectEvent](),
		reflect.TypeFor[extension.ThinkingLevelSelectEvent](),
		reflect.TypeFor[extension.UserBashEvent](),
		reflect.TypeFor[extension.InputEvent](),
		reflect.TypeFor[extension.CustomToolCallEvent](),
		reflect.TypeFor[extension.BashToolResultEvent](),
		reflect.TypeFor[extension.PowerShellToolResultEvent](),
		reflect.TypeFor[extension.ReadToolResultEvent](),
		reflect.TypeFor[extension.EditToolResultEvent](),
		reflect.TypeFor[extension.WriteToolResultEvent](),
		reflect.TypeFor[extension.GrepToolResultEvent](),
		reflect.TypeFor[extension.FindToolResultEvent](),
		reflect.TypeFor[extension.LsToolResultEvent](),
		reflect.TypeFor[extension.CustomToolResultEvent](),
	}
	// variants are the forms of an event its MarshalJSON writes differently.
	variants := map[string][]func(reflect.Value){
		"BeforeAgentStartEvent": {
			func(event reflect.Value) {
				event.FieldByName("MessageType").SetString(string(extension.BeforeAgentStartUser))
			},
			func(event reflect.Value) {
				event.FieldByName("MessageType").SetString(string(extension.BeforeAgentStartCustom))
			},
		},
	}
	eventInterface := reflect.TypeFor[extension.ExtensionEvent]()
	sweep := func(name string, event any, fill *sweepFill) {
		frame, err := sweepMarshal(event)
		var escaped, replaced, absent, other int
		var damaged []string
		labels := make([]string, 0, len(fill.paths))
		for label := range fill.paths {
			labels = append(labels, label)
		}
		slices.Sort(labels)
		for _, label := range labels {
			at := strings.Index(string(frame), label+"[")
			if at < 0 {
				absent++
				continue
			}
			rest := string(frame[at+len(label)+1:])
			switch {
			case strings.HasPrefix(rest, `\ud83d]`):
				escaped++
			case strings.HasPrefix(rest, strings.Repeat("\xef\xbf\xbd", 3)+"]"):
				replaced++
				damaged = append(damaged, fill.paths[label])
			default:
				other++
				damaged = append(damaged, fmt.Sprintf("%s (other: % x)", fill.paths[label], rest[:min(len(rest), 12)]))
			}
		}
		t.Logf("SWEEP|%s|text members %d|escape %d|three U+FFFD %d|other %d|not in the frame %d|damaged: %s|not filled: %s|err=%v", name, len(labels), escaped, replaced, other, absent,
			strings.Join(damaged, "; "), strings.Join(fill.unfilled, "; "), err)
		if err != nil {
			t.Errorf("%s: the event did not encode: %v", name, err)
		}
		if len(damaged) > 0 {
			t.Errorf("%s: %d text members are not written as the escape of the unit: %q", name, len(damaged), damaged)
		}
	}
	for _, eventType := range types {
		if eventType.Kind() != reflect.Struct {
			t.Errorf("%s is a %s, not a struct: the sweep does not fill it", eventType.Name(), eventType.Kind())
			continue
		}
		forms := variants[eventType.Name()]
		if forms == nil {
			forms = []func(reflect.Value){func(reflect.Value) {}}
		}
		for i, form := range forms {
			fill := &sweepFill{paths: map[string]string{}}
			target := reflect.New(eventType)
			fill.fill(target.Elem(), "", 0)
			form(target.Elem())
			event := target.Elem().Interface()
			if !eventType.Implements(eventInterface) {
				event = target.Interface()
			}
			name := eventType.Name()
			if len(forms) > 1 {
				name = fmt.Sprintf("%s#%d", name, i)
			}
			sweep(name, event, fill)
		}
	}
	// SystemPrompt encodes itself when a value holds it outside its event.
	fill := &sweepFill{paths: map[string]string{}}
	holder := struct {
		Prompt *extension.SystemPrompt `json:"prompt"`
	}{}
	fill.fill(reflect.ValueOf(&holder).Elem(), "", 0)
	sweep("SystemPrompt", holder, fill)
}
