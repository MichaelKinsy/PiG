package ai

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"
)

// Pi's declarationsEqual compares JSON.stringify of both declarations (utils/transcript.ts declarationsEqual). The in-place
// comparison must agree with that comparison for every pair: it may only answer true when the JSON is equal, and the
// marshaling path decides the rest, so DeclarationsEqual always equals the marshaled answer.
func TestDeclarationsEqualAgreesWithTheMarshaledComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(50))
	var tree func(depth int) any
	tree = func(depth int) any {
		switch rng.Intn(12) {
		case 0:
			return nil
		case 1:
			return rng.Intn(2) == 0
		case 2:
			return []string{"", "a", "b", "é", "\xff", "a\u2028b", "</script>"}[rng.Intn(7)]
		case 3:
			return []float64{0, math.Copysign(0, -1), 1, 1.5, -2, 1e21, 2.5e-7, 3}[rng.Intn(8)]
		case 4:
			return []int{0, 1, 2}[rng.Intn(3)]
		case 5:
			return int64(rng.Intn(3))
		case 6:
			return float32(rng.Intn(3))
		case 7, 8:
			if depth == 0 {
				return nil
			}
			if rng.Intn(5) == 0 {
				return map[string]any(nil)
			}
			object := map[string]any{}
			for range rng.Intn(4) {
				object[[]string{"type", "a", "b", "enum", "\xfe"}[rng.Intn(5)]] = tree(depth - 1)
			}
			if rng.Intn(6) == 0 {
				return JsonObject(object)
			}
			return object
		case 9, 10:
			if depth == 0 {
				return nil
			}
			if rng.Intn(5) == 0 {
				return []any(nil)
			}
			items := make([]any, rng.Intn(4))
			for i := range items {
				items[i] = tree(depth - 1)
			}
			return items
		default:
			return json.Number([]string{"1", "2", "1.0"}[rng.Intn(3)])
		}
	}
	mutate := func(tool ToolSchema) ToolSchema {
		out := cloneToolShallowForTest(tool)
		switch rng.Intn(8) {
		case 0:
			out.Description += "!"
		case 1:
			out.PromptGuidelines = []string{"different guideline"}
		case 2:
			out.ConstrainedSampling = &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema}
		case 3:
			out.ConstrainedSamplingDisabled = !out.ConstrainedSamplingDisabled
		case 4:
			if out.parameterOrder == nil {
				out.parameterOrder = schemaObjectOrder{}
			} else {
				out.parameterOrder = nil
			}
		case 5:
			out.parameterOrder = schemaObjectOrder{"": {"b", "a"}}
		}
		return out
	}
	trueCount, falseCount := 0, 0
	for range 20000 {
		left := ToolSchema{Name: "t", Description: "d", Parameters: map[string]any{"properties": tree(3), "x": tree(2)}, PromptGuidelines: []string{"g"}}
		if rng.Intn(10) == 0 {
			left.Parameters = nil
		}
		right := left
		if rng.Intn(3) != 0 {
			right = cloneTool(left)
		}
		if rng.Intn(2) == 0 {
			right = mutate(right)
		}
		got, want := DeclarationsEqual(left, right), declarationsEqualMarshaled(left, right)
		if got != want {
			t.Fatalf("DeclarationsEqual = %v, marshaled comparison = %v\nleft %#v\nright %#v", got, want, left, right)
		}
		if declarationsEqualPlain(left, right) && !want {
			t.Fatalf("the in-place comparison proved equal what marshals differently\nleft %#v\nright %#v", left, right)
		}
		if want {
			trueCount++
		} else {
			falseCount++
		}
	}
	if trueCount < 1000 || falseCount < 1000 {
		t.Fatalf("the generator exercised %d equal and %d different pairs; it must cover both", trueCount, falseCount)
	}
}

func cloneToolShallowForTest(tool ToolSchema) ToolSchema { return tool }

// Pairs the in-place comparison must not call equal although == or reflect.DeepEqual would: json.Marshal writes +0 and -0
// apart, a nil map as null and an empty one as {}, and Pi compares the serialized form.
func TestDeclarationsEqualKeepsWhatJSONKeepsApart(t *testing.T) {
	base := func(parameters map[string]any) ToolSchema { return ToolSchema{Name: "t", Parameters: parameters} }
	for _, tc := range []struct {
		name        string
		left, right ToolSchema
	}{
		{"negative zero", base(map[string]any{"v": 0.0}), base(map[string]any{"v": math.Copysign(0, -1)})},
		{"nil and empty parameters", base(nil), base(map[string]any{})},
		{"nil and empty nested object", base(map[string]any{"v": map[string]any(nil)}), base(map[string]any{"v": map[string]any{}})},
		{"nil and empty nested array", base(map[string]any{"v": []any(nil)}), base(map[string]any{"v": []any{}})},
		{"int and float of one value", base(map[string]any{"v": 1}), base(map[string]any{"v": 1.0})},
		{"explicit constrainedSampling false", ToolSchema{Name: "t", ConstrainedSamplingDisabled: true}, ToolSchema{Name: "t"}},
		// Imported parameter orders write the same members in different orders.
		{"different imported parameter orders", ToolSchema{Name: "t", Parameters: map[string]any{"a": 1.0, "b": 2.0}, parameterOrder: schemaObjectOrder{"": {"a", "b"}}}, ToolSchema{Name: "t", Parameters: map[string]any{"a": 1.0, "b": 2.0}, parameterOrder: schemaObjectOrder{"": {"b", "a"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := declarationsEqualMarshaled(tc.left, tc.right)
			if got := DeclarationsEqual(tc.left, tc.right); got != want {
				t.Fatalf("DeclarationsEqual = %v, marshaled comparison %v", got, want)
			}
			if declarationsEqualPlain(tc.left, tc.right) && !want {
				t.Fatal("the in-place comparison called a JSON difference equal")
			}
		})
	}
}

func TestInPlaceComparisonDoesNotRecurseThroughACycle(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	tool := ToolSchema{Name: "t", Parameters: cyclic}
	if declarationsEqualPlain(tool, tool) {
		t.Fatal("the in-place comparison vouched for a cycle")
	}
}

// A value json.Marshal rejects makes DeclarationsEqual panic in ToToolDeclaration's copy, before and after the in-place
// comparison; the comparison never turns that panic into an answer.
func TestDeclarationsEqualPanicsOnUnserializableValuesAsBefore(t *testing.T) {
	for name, value := range map[string]any{"NaN": math.NaN(), "Inf": math.Inf(1), "channel": make(chan int), "cycle": cyclicObject()} {
		t.Run(name, func(t *testing.T) {
			tool := ToolSchema{Name: "t", Parameters: map[string]any{"v": value}}
			defer func() {
				if recover() == nil {
					t.Fatal("DeclarationsEqual answered for a declaration that cannot be serialized")
				}
			}()
			DeclarationsEqual(tool, tool)
		})
	}
}

func cyclicObject() map[string]any {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	return cyclic
}

// GetToolStateChangesFrom is GetToolStateChanges over the replayed tools without copying them. Replays with additions,
// removals, redefinitions and re-additions must give the same delta as the copying pair, and neither the transcript nor the
// current tools may change.
func TestGetToolStateChangesFromMatchesTheCopyingReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	tool := func(name string, variant int) ToolSchema {
		return ToolSchema{Name: name, Description: "d", Parameters: map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": "string", "default": variant}}}, PromptGuidelines: []string{"g"}}
	}
	names := []string{"read", "write", "edit", "bash"}
	for range 500 {
		var transcript []Message
		for range rng.Intn(6) {
			system := SystemMessage{Content: SystemText("")}
			for range rng.Intn(3) {
				system.ToolsAdded = append(system.ToolsAdded, tool(names[rng.Intn(4)], rng.Intn(2)))
			}
			for range rng.Intn(2) {
				system.ToolsRemoved = append(system.ToolsRemoved, ToolReference{Name: names[rng.Intn(4)]})
			}
			transcript = append(transcript, system, UserMessage{Content: UserText("hi")})
		}
		var current []ToolSchema
		for _, name := range names {
			if rng.Intn(2) == 0 {
				current = append(current, tool(name, rng.Intn(2)))
			}
		}
		before := cloneTools(current)
		want := GetToolStateChanges(GetCurrentTools(transcript), current)
		got := GetToolStateChangesFrom(transcript, current)
		if len(got.ToolsAdded) != len(want.ToolsAdded) || len(got.ToolsRemoved) != len(want.ToolsRemoved) {
			t.Fatalf("delta = %+v, copying replay %+v", got, want)
		}
		for i := range want.ToolsAdded {
			if !declarationsEqualMarshaled(got.ToolsAdded[i], want.ToolsAdded[i]) {
				t.Fatalf("added tool %d = %+v, copying replay %+v", i, got.ToolsAdded[i], want.ToolsAdded[i])
			}
		}
		for i := range want.ToolsRemoved {
			if got.ToolsRemoved[i] != want.ToolsRemoved[i] {
				t.Fatalf("removed tool %d = %+v, copying replay %+v", i, got.ToolsRemoved[i], want.ToolsRemoved[i])
			}
		}
		for i := range current {
			if !declarationsEqualMarshaled(current[i], before[i]) {
				t.Fatalf("current tool %d changed", i)
			}
		}
		for _, added := range got.ToolsAdded {
			if len(added.PromptGuidelines) != 0 {
				t.Fatalf("an added declaration kept prompt guidelines: %+v", added)
			}
		}
	}
}

// The delta carries copies: modifying an added declaration must not reach the transcript the replay shared.
func TestGetToolStateChangesFromReturnsCopiesOfAddedTools(t *testing.T) {
	declared := ToolSchema{Name: "read", Parameters: map[string]any{"type": "object"}}
	transcript := []Message{SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{declared}}}
	changed := ToolSchema{Name: "read", Description: "changed", Parameters: map[string]any{"type": "object"}}
	delta := GetToolStateChangesFrom(transcript, []ToolSchema{changed})
	if len(delta.ToolsAdded) != 1 {
		t.Fatalf("delta = %+v", delta)
	}
	delta.ToolsAdded[0].Parameters["type"] = "mutated"
	if changed.Parameters["type"] != "object" || declared.Parameters["type"] != "object" {
		t.Fatal("an added declaration shares storage with the current tool or the transcript")
	}
}

// The path of containers under comparison keeps its first levels inline and the rest in a slice; a tree deeper than the inline
// part must compare and detect a cycle as a shallow one does.
func TestInPlaceComparisonHandlesTreesDeeperThanTheInlinePath(t *testing.T) {
	deep := func(levels int) map[string]any {
		root := map[string]any{}
		node := root
		for range levels {
			next := map[string]any{"a": []any{"x"}}
			node["child"] = next
			node = next
		}
		return root
	}
	for _, levels := range []int{1, 7, 8, 9, 40} {
		left, right := ToolSchema{Name: "t", Parameters: deep(levels)}, ToolSchema{Name: "t", Parameters: deep(levels)}
		if !declarationsEqualPlain(left, right) {
			t.Fatalf("equal trees %d levels deep were not proven equal", levels)
		}
		right.Parameters["child"].(map[string]any)["a"] = []any{"y"}
		if declarationsEqualPlain(left, right) {
			t.Fatalf("trees %d levels deep that differ at the first level were called equal", levels)
		}
	}
	cyclic := deep(12)
	node := cyclic
	for range 12 {
		node = node["child"].(map[string]any)
	}
	node["back"] = cyclic
	tool := ToolSchema{Name: "t", Parameters: cyclic}
	if declarationsEqualPlain(tool, tool) {
		t.Fatal("the in-place comparison vouched for a cycle below the inline path")
	}
}

// A container reachable twice by different paths is shared, not cyclic, at any depth: leaving a level forgets it.
func TestInPlaceComparisonAcceptsASharedSubtreeBelowTheInlinePath(t *testing.T) {
	build := func() map[string]any {
		shared := map[string]any{"leaf": []any{"x"}}
		node := map[string]any{"first": shared, "second": shared}
		for range 10 {
			node = map[string]any{"down": node}
		}
		return node
	}
	left, right := ToolSchema{Name: "t", Parameters: build()}, ToolSchema{Name: "t", Parameters: build()}
	if !declarationsEqualPlain(left, right) {
		t.Fatal("a shared subtree was taken for a cycle")
	}
}
