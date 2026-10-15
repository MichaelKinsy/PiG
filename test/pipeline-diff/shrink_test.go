package pipelinediff

import (
	"encoding/json"
	"regexp"
	"slices"
)

var indexPattern = regexp.MustCompile(`\[\d+\]`)

// signature identifies the kind of a difference: the outcome kind and the path of the first differing member without array positions.
func signature(o outcome) string {
	if o.Kind != "differs" {
		return o.Kind
	}
	path := o.Detail
	for i := 0; i+1 < len(path); i++ {
		if path[i] == ':' && path[i+1] == ' ' {
			path = path[:i]
			break
		}
	}
	return "differs " + indexPattern.ReplaceAllString(path, "[]")
}

func cloneCase(c testCase) testCase {
	encoded, _ := json.Marshal(c)
	var out testCase
	_ = json.Unmarshal(encoded, &out)
	return out
}

// shrink reduces a failing case to a smaller one that still fails with the same signature. It removes messages, then content blocks, to a fixed point.
func shrink(c testCase, still func(testCase) bool) testCase {
	best := cloneCase(c)
	for changed := true; changed; {
		changed = false
		for chunk := len(best.Messages) / 2; chunk >= 1; chunk /= 2 {
			for start := 0; start+chunk <= len(best.Messages); {
				candidate := cloneCase(best)
				candidate.Messages = slices.Delete(candidate.Messages, start, start+chunk)
				if still(candidate) {
					best, changed = candidate, true
					continue
				}
				start++
			}
		}
		for i := range best.Messages {
			content, ok := best.Messages[i]["content"].([]any)
			if !ok {
				continue
			}
			for j := 0; j < len(content); {
				candidate := cloneCase(best)
				blocks := candidate.Messages[i]["content"].([]any)
				candidate.Messages[i]["content"] = slices.Delete(blocks, j, j+1)
				if still(candidate) {
					best, changed = candidate, true
					content = best.Messages[i]["content"].([]any)
					continue
				}
				j++
			}
		}
		if best.SystemPrompt != "" {
			candidate := cloneCase(best)
			candidate.SystemPrompt = ""
			if still(candidate) {
				best, changed = candidate, true
			}
		}
	}
	return best
}
