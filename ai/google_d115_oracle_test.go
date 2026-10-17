package ai_test

import (
	"encoding/json"
)

// piPayloadWithD115 rewrites Pi's Google payload into the shape D115 specifies: within a run of function-response turns and Gemini 2.x image turns ("Tool result image:"), every function response joins the run's first turn and the image turns follow it in order. Pi's order is google-shared.ts convertMessages; the payload oracles compare PiG with this rewrite.
func piPayloadWithD115(payload string) string {
	var body map[string]any
	if json.Unmarshal([]byte(payload), &body) != nil {
		return payload
	}
	contents, ok := body["contents"].([]any)
	if !ok {
		return payload
	}
	turnKind := func(turn any) string {
		m, _ := turn.(map[string]any)
		parts, _ := m["parts"].([]any)
		if m["role"] != "user" || len(parts) == 0 {
			return ""
		}
		first, _ := parts[0].(map[string]any)
		if _, ok := first["functionResponse"]; ok {
			return "response"
		}
		if first["text"] == "Tool result image:" {
			return "image"
		}
		return ""
	}
	var out, images []any
	var run map[string]any
	flush := func() {
		out = append(out, images...)
		images, run = nil, nil
	}
	for _, turn := range contents {
		switch turnKind(turn) {
		case "response":
			if run == nil {
				run = turn.(map[string]any)
				out = append(out, turn)
				continue
			}
			run["parts"] = append(run["parts"].([]any), turn.(map[string]any)["parts"].([]any)...)
		case "image":
			if run == nil {
				out = append(out, turn)
				continue
			}
			images = append(images, turn)
		default:
			flush()
			out = append(out, turn)
		}
	}
	flush()
	body["contents"] = out
	encoded, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return string(encoded)
}
