package telemetry

// Ports packages/telemetry/src/render.ts.

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// formatAttributes is upstream's formatAttributes: Object.entries in insertion order.
func formatAttributes(attributes SpanAttributes) string {
	var text strings.Builder
	for _, attribute := range attributes {
		text.WriteString(" " + attribute.Name + "=" + jsonStringify(attribute.Value))
	}
	return text.String()
}

// jsonStringify is JSON.stringify of one attribute value: a string, boolean or number, or an array of them.
func jsonStringify(value AttributeValue) string {
	var text strings.Builder
	writeJSON(&text, reflect.ValueOf(value))
	return text.String()
}

func writeJSON(text *strings.Builder, value reflect.Value) {
	switch value.Kind() {
	case reflect.String:
		writeJSONString(text, value.String())
	case reflect.Bool:
		text.WriteString(strconv.FormatBool(value.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		text.WriteString(jsnumber.String(float64(value.Int())))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		text.WriteString(jsnumber.String(float64(value.Uint())))
	case reflect.Float32, reflect.Float64:
		if number := value.Float(); math.IsNaN(number) || math.IsInf(number, 0) {
			text.WriteString("null")
		} else {
			text.WriteString(jsnumber.String(number))
		}
	case reflect.Slice, reflect.Array:
		text.WriteByte('[')
		for index := range value.Len() {
			if index > 0 {
				text.WriteByte(',')
			}
			writeJSON(text, value.Index(index))
		}
		text.WriteByte(']')
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			text.WriteString("null")
			return
		}
		writeJSON(text, value.Elem())
	default:
		text.WriteString("null")
	}
}

// writeJSONString is JSON.stringify of a string: quotes, backslashes and control characters are escaped, and every
// other character, U+2028 and U+2029 included, is written as is.
func writeJSONString(text *strings.Builder, value string) {
	text.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			text.WriteString(`\"`)
		case '\\':
			text.WriteString(`\\`)
		case '\b':
			text.WriteString(`\b`)
		case '\f':
			text.WriteString(`\f`)
		case '\n':
			text.WriteString(`\n`)
		case '\r':
			text.WriteString(`\r`)
		case '\t':
			text.WriteString(`\t`)
		default:
			if character < 0x20 {
				fmt.Fprintf(text, `\u%04x`, character)
			} else {
				text.WriteRune(character)
			}
		}
	}
	text.WriteByte('"')
}

// RenderTrace renders stored spans as a chronological forest, treating absent parents as roots. Spans sharing an ID are
// one span, the last value in the first position, as upstream's Map keeps it; the input is
// not modified.
func RenderTrace(spans []StoredSpan) string {
	byID := map[string]StoredSpan{}
	var order []string
	for _, span := range spans {
		if _, ok := byID[span.ID]; !ok {
			order = append(order, span.ID)
		}
		byID[span.ID] = span
	}
	sorted := make([]StoredSpan, 0, len(order))
	for _, id := range order {
		sorted = append(sorted, byID[id])
	}
	sort.SliceStable(sorted, func(left, right int) bool { return sorted[left].StartedMs < sorted[right].StartedMs })

	var roots []StoredSpan
	children := map[string][]StoredSpan{}
	for _, span := range sorted {
		if span.ParentID == nil {
			roots = append(roots, span)
		} else if _, ok := byID[*span.ParentID]; !ok {
			roots = append(roots, span)
		} else {
			children[*span.ParentID] = append(children[*span.ParentID], span)
		}
	}

	type pendingSpan struct {
		span  StoredSpan
		depth int
	}
	var lines []string
	slices.Reverse(roots)
	pending := make([]pendingSpan, 0, len(roots))
	for _, root := range roots {
		pending = append(pending, pendingSpan{root, 0})
	}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		span, indent := current.span, strings.Repeat("  ", current.depth)
		failure := ""
		if span.Status.Status == SpanStatusCodeError {
			failure = " error"
			if span.Status.Error != nil {
				failure += " " + span.Status.Error.Name + ": " + span.Status.Error.Message
			}
		}
		lines = append(lines, indent+span.Name+formatAttributes(span.Attributes)+" ("+jsnumber.String(span.EndedMs-span.StartedMs)+"ms)"+failure)
		for _, event := range span.Events {
			lines = append(lines, indent+"  · "+event.Name+formatAttributes(event.Attributes)+" (+"+jsnumber.String(event.TimeMs)+"ms)")
		}
		for _, link := range span.Links {
			lines = append(lines, indent+"  → link trace "+link.TraceID+" span "+link.SpanID+formatAttributes(link.Attributes))
		}
		descendants := children[span.ID]
		for _, descendant := range slices.Backward(descendants) {
			pending = append(pending, pendingSpan{descendant, current.depth + 1})
		}
	}
	return strings.Join(lines, "\n")
}
