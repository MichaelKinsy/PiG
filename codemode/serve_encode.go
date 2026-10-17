package codemode

import (
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The JSON of the exchanges the Worker module sends its host, written by hand into one buffer. encoding/json would do, but it
// builds each message in its own buffers and copies the result, and measured in the module (a script's 16 MiB of output) that cost
// several times their size in a 96 MiB heap; this writes each string once. The shapes are those of RemoteMessage.MarshalJSON
// (packages/codemode/src/runtime/protocol.ts and remote.ts). Strings go through jsstring, which keeps the UTF-16 units of a
// JavaScript string, a lone surrogate included, where encoding/json writes U+FFFD.

// appendExchange appends {"id":id,"take":n,"messages":[...]}. take is the number of output items the host holds that go in front
// of the messages (the placeholders messageHostedOutput stands for); the messages are the others.
func appendExchange(dst []byte, id int, messages []RemoteMessage) []byte {
	take := 0
	for _, message := range messages {
		if message.Type == messageHostedOutput {
			take += message.ID
		}
	}
	dst = append(dst, `{"id":`...)
	dst = strconv.AppendInt(dst, int64(id), 10)
	dst = append(dst, `,"take":`...)
	dst = strconv.AppendInt(dst, int64(take), 10)
	dst = append(dst, `,"messages":[`...)
	first := true
	for _, message := range messages {
		if message.Type == messageHostedOutput {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = appendRemoteMessage(dst, message)
	}
	return append(dst, "]}"...)
}

func appendField(dst []byte, name, value string) []byte {
	dst = append(dst, ',', '"')
	dst = append(dst, name...)
	dst = append(dst, `":`...)
	return jsstring.AppendString(dst, value)
}

func appendOptionalField(dst []byte, name string, value *string) []byte {
	if value == nil {
		return dst
	}
	return appendField(dst, name, *value)
}

// appendRemoteMessage appends one message in upstream's shape: the fields that apply to its type.
func appendRemoteMessage(dst []byte, m RemoteMessage) []byte {
	dst = append(dst, `{"type":`...)
	dst = jsstring.AppendString(dst, m.Type)
	switch m.Type {
	case MessageCall:
		dst = append(dst, `,"id":`...)
		dst = strconv.AppendInt(dst, int64(m.ID), 10)
		dst = appendField(dst, "target", m.Target)
		dst = appendField(dst, "name", m.Name)
		dst = appendOptionalField(dst, "args", m.Args)
	case MessageOutput:
		dst = append(dst, `,"item":`...)
		dst = appendOutputItem(dst, m.Item)
	case MessageDone:
		dst = append(dst, `,"ok":`...)
		dst = strconv.AppendBool(dst, m.Ok)
		dst = appendOptionalField(dst, "value", m.Value)
		dst = appendOptionalField(dst, "writes", m.Writes)
		dst = appendOptionalField(dst, "error", m.Error)
		if m.During != nil {
			dst = append(dst, `,"during":{"seq":`...)
			dst = strconv.AppendInt(dst, int64(m.During.Seq), 10)
			dst = append(dst, `,"issued":`...)
			dst = strconv.AppendInt(dst, int64(m.During.Issued), 10)
			dst = append(dst, '}')
		}
	case MessageStarted:
		dst = appendField(dst, "fingerprint", m.Fingerprint)
	case MessageIdle:
		dst = append(dst, `,"seq":`...)
		dst = strconv.AppendInt(dst, int64(m.Seq), 10)
		dst = append(dst, `,"issued":`...)
		dst = strconv.AppendInt(dst, int64(m.Issued), 10)
	case MessageCrash:
		dst = appendField(dst, "message", m.Message)
	case MessageFailure:
		dst = append(dst, `,"error":`...)
		dst = appendError(dst, m.Failure)
	}
	return append(dst, '}')
}

// appendOutputItem writes the item as outputItemToWire does: every field a text or image item requires, also when empty.
func appendOutputItem(dst []byte, item *OutputItem) []byte {
	if item == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, `{"type":`...)
	dst = jsstring.AppendString(dst, string(item.Type))
	switch item.Type {
	case OutputItemText:
		dst = appendField(dst, "text", item.Text)
		if item.Console {
			dst = append(dst, `,"console":true`...)
		}
	case OutputItemImage:
		dst = appendField(dst, "data", item.Data)
		dst = appendField(dst, "mimeType", item.MimeType)
	default:
		if item.Text != "" {
			dst = appendField(dst, "text", item.Text)
		}
		if item.Console {
			dst = append(dst, `,"console":true`...)
		}
		if item.Data != "" {
			dst = appendField(dst, "data", item.Data)
		}
		if item.MimeType != "" {
			dst = appendField(dst, "mimeType", item.MimeType)
		}
	}
	return append(dst, '}')
}

// appendError writes an Error as its JSON tags do: kind and message always, name and stack when not empty.
func appendError(dst []byte, e *Error) []byte {
	if e == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, `{"kind":`...)
	dst = jsstring.AppendString(dst, string(e.Kind))
	if e.Name != "" {
		dst = appendField(dst, "name", e.Name)
	}
	dst = appendField(dst, "message", e.Message)
	if e.Stack != "" {
		dst = appendField(dst, "stack", e.Stack)
	}
	return append(dst, '}')
}
