package ai

import (
	"regexp"
	"sync/atomic"
)

// pig divergence (D236): some models write a text property as `"newText">` instead of `"newText": "`:
// the colon, the space and the value's opening quote become
// a literal '>', as if closing an XML tag. The rest of the arguments text, including the value's closing quote, is
// intact, so the rewrite is exact. It applies only when the text is not valid JSON, only to the three
// multi-line text properties, and only where a property name can start (after '{' or ','), never inside a string.
var tagArrowProperty = regexp.MustCompile(`([{,]\s*)"(oldText|newText|content)">`)

// ToolArgumentsRepair describes one repaired tool call for the repair hook.
type ToolArgumentsRepair struct {
	Kind  string   // "tag-arrow"
	Count int      // properties rewritten
	Keys  []string // property names rewritten, in order
}

var toolArgumentsRepairHook atomic.Pointer[func(ToolArgumentsRepair)]

// SetToolArgumentsRepairHook installs fn (nil removes it); it is called once per repaired tool call.
func SetToolArgumentsRepairHook(fn func(ToolArgumentsRepair)) {
	if fn == nil {
		toolArgumentsRepairHook.Store(nil)
		return
	}
	toolArgumentsRepairHook.Store(&fn)
}

// repairTagArrowProperties returns the rewritten text and the rewritten property names, or text unchanged and nil.
func repairTagArrowProperties(text string) (string, []string) {
	matches := tagArrowProperty.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	keys := make([]string, 0, len(matches))
	for _, m := range matches {
		keys = append(keys, m[2])
	}
	return tagArrowProperty.ReplaceAllString(text, `$1"$2": "`), keys
}

func reportToolArgumentsRepair(r ToolArgumentsRepair) {
	if fn := toolArgumentsRepairHook.Load(); fn != nil {
		(*fn)(r)
	}
}
