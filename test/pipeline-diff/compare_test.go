package pipelinediff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// messageFields are the top-level members of each API's request body that carry the converted conversation. The other members (sampling, tools, cache keys) come from options both sides set alike and are not this pipeline's output.
var messageFields = map[string][]string{
	"anthropic-messages":     {"messages", "system"},
	"openai-responses":       {"input"},
	"azure-openai-responses": {"input"},
	"openai-completions":     {"messages"},
	"google-generative-ai":   {"contents", "systemInstruction"},
	"mistral-conversations":  {"messages"},
}

// projection reduces a request body to its conversation members.
func projection(api string, body []byte) (map[string]any, error) {
	var all map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&all); err != nil {
		return nil, fmt.Errorf("request body is not JSON: %w", err)
	}
	out := map[string]any{}
	for _, field := range messageFields[api] {
		if value, ok := all[field]; ok {
			out[field] = value
		}
	}
	return out, nil
}

// difference describes where two projections first differ, or "" when equal.
func difference(pi, pig map[string]any) string {
	if reflect.DeepEqual(pi, pig) {
		return ""
	}
	return firstDifference("", pi, pig)
}

func firstDifference(path string, a, b any) string {
	switch a := a.(type) {
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: pi %s, pig %s", path, short(a), short(b))
		}
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			av, aok := a[k]
			bv, bok := b[k]
			switch {
			case !aok:
				return fmt.Sprintf("%s.%s: pi absent, pig %s", path, k, short(bv))
			case !bok:
				return fmt.Sprintf("%s.%s: pi %s, pig absent", path, k, short(av))
			}
			if d := firstDifference(path+"."+k, av, bv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		b, ok := b.([]any)
		if !ok {
			return fmt.Sprintf("%s: pi %s, pig %s", path, short(a), short(b))
		}
		for i := 0; i < len(a) && i < len(b); i++ {
			if d := firstDifference(fmt.Sprintf("%s[%d]", path, i), a[i], b[i]); d != "" {
				return d
			}
		}
		if len(a) != len(b) {
			return fmt.Sprintf("%s: pi has %d entries, pig has %d (pi %s | pig %s)", path, len(a), len(b), short(a), short(b))
		}
		return ""
	default:
		if reflect.DeepEqual(a, b) {
			return ""
		}
		return fmt.Sprintf("%s: pi %s, pig %s", path, short(a), short(b))
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func short(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return strings.TrimSpace(s)
}
