package orderedjson

import "testing"

// A member the source lists keeps its place and a new member follows it, also when the source is already in sorted order: an assignment to a JavaScript object appends, whatever the key sorts as.
func TestMarshalInSourceOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		value  any
		source string
		want   string
	}{
		"added member follows a sorted source": {map[string]any{"command": "x", "timeout": 5.0, "aaa": 1.0}, `{"command":"y","timeout":5}`, `{"command":"x","timeout":5,"aaa":1}`},
		"unsorted source":                      {map[string]any{"a": 1.0, "b": 2.0, "z": 3.0}, `{"z":0,"b":0,"a":0}`, `{"z":3,"b":2,"a":1}`},
		"removed member":                       {map[string]any{"b": 2.0}, `{"z":0,"b":0,"a":0}`, `{"b":2}`},
		"two added members are sorted":         {map[string]any{"k": 1.0, "j": 2.0, "b": 3.0}, `{"k":0}`, `{"k":1,"b":3,"j":2}`},
		"integer-like keys come first":         {map[string]any{"b": 1.0, "10": 2.0, "2": 3.0, "01": 4.0}, `{"b":0}`, `{"2":3,"10":2,"b":1,"01":4}`},
		"nested object and array":              {map[string]any{"o": map[string]any{"a": 1.0, "z": 2.0, "m": 3.0}, "l": []any{map[string]any{"b": 1.0, "a": 2.0}, 7.0}}, `{"o":{"z":0,"a":0},"l":[{"b":0}]}`, `{"o":{"z":2,"a":1,"m":3},"l":[{"b":1,"a":2},7]}`},
		"no source":                            {map[string]any{"b": 1.0, "a": 2.0}, ``, `{"a":2,"b":1}`},
		"source of another shape":              {map[string]any{"b": 1.0, "a": 2.0}, `[1]`, `{"a":2,"b":1}`},
		"html is not escaped":                  {map[string]any{"t": "<a&b>"}, `{}`, `{"t":"<a&b>"}`},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := MarshalInSourceOrder(tc.value, []byte(tc.source))
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}
