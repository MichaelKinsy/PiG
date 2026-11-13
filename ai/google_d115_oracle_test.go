package ai_test

import (
	"bytes"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// piPayloadWithD115 rewrites Pi's Google payload into the shape D115 specifies: within a run of function-response turns and Gemini 2.x image turns ("Tool result image:"), every function response joins the run's first turn and the image turns follow it in order. Pi's order is google-shared.ts convertMessages; the payload oracles compare PiG with this rewrite. The rewrite moves turns and parts as the text Pi wrote: every member of the payload stays in Pi's order, so the comparison is one of bytes.
func piPayloadWithD115(payload string) string {
	body, err := orderedjson.Parse([]byte(payload))
	if err != nil {
		return payload
	}
	rawContents, ok := body.Get("contents")
	var contents []json.RawMessage
	if !ok || json.Unmarshal(rawContents, &contents) != nil {
		return payload
	}
	type turn struct {
		raw   json.RawMessage
		parts []json.RawMessage
		// joined is set once another turn's parts were appended.
		joined bool
	}
	turnKind := func(raw json.RawMessage) (string, []json.RawMessage) {
		var members struct {
			Role  string            `json:"role"`
			Parts []json.RawMessage `json:"parts"`
		}
		if json.Unmarshal(raw, &members) != nil || members.Role != "user" || len(members.Parts) == 0 {
			return "", nil
		}
		var first map[string]json.RawMessage
		if json.Unmarshal(members.Parts[0], &first) != nil {
			return "", nil
		}
		if _, ok := first["functionResponse"]; ok {
			return "response", members.Parts
		}
		if string(first["text"]) == `"Tool result image:"` {
			return "image", members.Parts
		}
		return "", nil
	}
	var out, images []*turn
	var run *turn
	changed := false
	flush := func() {
		out = append(out, images...)
		images, run = nil, nil
	}
	for _, raw := range contents {
		kind, parts := turnKind(raw)
		switch kind {
		case "response":
			if run == nil {
				run = &turn{raw: raw, parts: parts}
				out = append(out, run)
				continue
			}
			run.parts, run.joined, changed = append(run.parts, parts...), true, true
		case "image":
			if run == nil {
				out = append(out, &turn{raw: raw})
				continue
			}
			images = append(images, &turn{raw: raw})
		default:
			flush()
			out = append(out, &turn{raw: raw})
		}
	}
	flush()
	if !changed {
		// Without a joined turn every image turn already follows its run's only response turn.
		return payload
	}
	texts := make([][]byte, len(out))
	for i, item := range out {
		texts[i] = item.raw
		if !item.joined {
			continue
		}
		object, err := orderedjson.Parse(item.raw)
		if err != nil {
			return payload
		}
		object.Set("parts", json.RawMessage("["+string(bytes.Join(rawSlices(item.parts), []byte(",")))+"]"))
		if texts[i], err = object.MarshalJSON(); err != nil {
			return payload
		}
	}
	body.Set("contents", json.RawMessage("["+string(bytes.Join(texts, []byte(",")))+"]"))
	encoded, err := body.MarshalJSON()
	if err != nil {
		return payload
	}
	return string(encoded)
}

func rawSlices(items []json.RawMessage) [][]byte {
	out := make([][]byte, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}
