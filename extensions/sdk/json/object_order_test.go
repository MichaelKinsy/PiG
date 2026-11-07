package json

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// orderOf is the keys, in order, of every object of m with two members or more, by the path to it: the order the decode recorded for the object the path
// reaches.
func orderOf(t *testing.T, order *ObjectOrder, value any, path string, out map[string][]string) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		if keys, ok := order.KeysOf(v); ok {
			out[path] = append([]string(nil), keys...)
		}
		for key, member := range v {
			orderOf(t, order, member, path+"/"+key, out)
		}
	case []any:
		for i, member := range v {
			orderOf(t, order, member, path+"/"+string(rune('0'+i)), out)
		}
	}
}

func TestUnmarshalObjectRecordsTheMemberOrder(t *testing.T) {
	long := make([]string, 0, 40)
	var wide strings.Builder
	wide.WriteString(`{`)
	for i := range 40 {
		key := string(rune('a'+i%26)) + string(rune('A'+i/26))
		long = append(long, key)
	}
	// 40 keys in reverse byte order, then a repeat of the first and of the last.
	for i := len(long) - 1; i >= 0; i-- {
		wide.WriteString(`"` + long[i] + `":1,`)
	}
	wide.WriteString(`"` + long[39] + `":2,"` + long[0] + `":3}`)
	wantWide := make([]string, 0, 40)
	for i := len(long) - 1; i >= 0; i-- {
		wantWide = append(wantWide, long[i])
	}

	for _, test := range []struct {
		name, text string
		want       map[string][]string
	}{
		{"flat", `{"z":1,"a":2,"m":3}`, map[string][]string{"": {"z", "a", "m"}}},
		{"nested", `{"z":{"y":1,"b":[{"q":1,"c":2},{"d":3,"a":4}]},"a":2}`, map[string][]string{
			"": {"z", "a"}, "/z": {"y", "b"}, "/z/b/0": {"q", "c"}, "/z/b/1": {"d", "a"},
		}},
		{"an object of one member has an order, as a member added to it follows it; an empty object has none", `{"a":{"b":1},"c":{},"d":[{}]}`, map[string][]string{"": {"a", "c", "d"}, "/a": {"b"}}},
		{"a repeated key keeps its first place and its last value", `{"b":1,"a":2,"b":3,"c":4,"a":5}`, map[string][]string{"": {"b", "a", "c"}}},
		{"keys that are escaped, empty, or not ASCII", `{"\u00e9":1,"":2,"a\"q":3,"é":4,"\n":5}`, map[string][]string{"": {"é", "", `a"q`, "\n"}}},
		{"keys that look like integers keep the text's order", `{"10":1,"9":2,"a":3,"0":4}`, map[string][]string{"": {"10", "9", "a", "0"}}},
		{"an object past the scan limit of repeated keys", wide.String(), map[string][]string{"": wantWide}},
		{"an empty object", `{}`, map[string][]string{}},
		{"white space", " \n{ \"b\" : { \"y\" : 1 , \"x\" : 2 } ,\t\"a\" : 1 }\n", map[string][]string{"": {"b", "a"}, "/b": {"y", "x"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, decode := range []func([]byte) (map[string]any, *ObjectOrder, error){UnmarshalObject, UnmarshalTrustedObject} {
				value, order, err := decode([]byte(test.text))
				if err != nil {
					t.Fatal(err)
				}
				var want map[string]any
				if err := Unmarshal([]byte(test.text), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(value, want) {
					t.Errorf("value\n got %#v\nwant %#v", value, want)
				}
				got := map[string][]string{}
				orderOf(t, order, value, "", got)
				if !reflect.DeepEqual(got, test.want) {
					t.Errorf("order\n got %q\nwant %q", got, test.want)
				}
			}
		})
	}
}

// A decode of anything but an object leaves the map nil with what Unmarshal reports, and knows no order.
func TestUnmarshalObjectOfAValueThatIsNotAnObject(t *testing.T) {
	for _, text := range []string{`null`, `[1,2]`, `"s"`, `3`, `true`, ` null `} {
		var want map[string]any
		wantErr := Unmarshal([]byte(text), &want)
		got, order, err := UnmarshalObject([]byte(text))
		if got != nil || order != nil || (err == nil) != (wantErr == nil) {
			t.Errorf("%s: got %v, %v, %v; Unmarshal gives %v", text, got, order, err, wantErr)
		}
		if _, ok := order.Keys(1); ok {
			t.Errorf("%s: a nil order knows an object", text)
		}
	}
	for _, text := range []string{``, `{`, `{"a":1,}`, `{"a":1} {"b":2}`} {
		if _, _, err := UnmarshalObject([]byte(text)); err == nil {
			t.Errorf("%q: no error for invalid JSON", text)
		}
	}
}

// Objects are known by their own maps: a copy is not the object, a map the order holds is not freed while it does, and Carry gives a copy its original's order.
func TestObjectOrderIdentifiesObjectsByTheirMaps(t *testing.T) {
	value, order, err := UnmarshalObject([]byte(`{"x":{"z":1,"a":2},"y":{"z":1,"a":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	x, y := value["x"].(map[string]any), value["y"].(map[string]any)
	if keys, ok := order.KeysOf(x); !ok || keys[0] != "z" {
		t.Errorf("x = %v, %v", keys, ok)
	}
	copied := map[string]any{"z": 1, "a": 2}
	if _, ok := order.KeysOf(copied); ok {
		t.Error("a copy of an object has the order of the original")
	}
	if _, ok := order.KeysOf(nil); ok {
		t.Error("a nil map has an order")
	}
	carried := new(ObjectOrder)
	carried.Carry(order, x, copied)
	carried.Carry(order, map[string]any{"q": 1, "r": 2}, map[string]any{})
	if keys, ok := carried.KeysOf(copied); !ok || keys[0] != "z" {
		t.Errorf("the carried copy = %v, %v", keys, ok)
	}
	if _, ok := carried.KeysOf(y); ok {
		t.Error("an object that was not carried has an order")
	}
	// Sorting for the lookups does not change what an order knows.
	if keys, ok := order.KeysOf(y); !ok || keys[0] != "z" {
		t.Errorf("y = %v, %v", keys, ok)
	}
}

// The keys an order hands out are never rewritten by what is recorded after them: a thousand objects span several chunks of keys.
func TestObjectOrderKeepsEveryObjectsKeysWhole(t *testing.T) {
	var text strings.Builder
	text.WriteString(`{"list":[`)
	for i := range 3000 {
		if i > 0 {
			text.WriteByte(',')
		}
		text.WriteString(`{"k` + strings.Repeat("x", i%7) + `":1,"b":2,"a":{"n":1,"m":2}}`)
	}
	text.WriteString(`]}`)
	value, order, err := UnmarshalObject([]byte(text.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i, element := range value["list"].([]any) {
		object := element.(map[string]any)
		if keys, ok := order.KeysOf(object); !ok || !reflect.DeepEqual(keys, []string{"k" + strings.Repeat("x", i%7), "b", "a"}) {
			t.Fatalf("object %d: %v, %v", i, keys, ok)
		}
		if keys, ok := order.KeysOf(object["a"].(map[string]any)); !ok || !reflect.DeepEqual(keys, []string{"n", "m"}) {
			t.Fatalf("object %d, inner: %v, %v", i, keys, ok)
		}
	}
	// An object with more keys than a chunk holds takes a chunk of its own.
	var wide strings.Builder
	wide.WriteString(`{"first":{"b":1,"a":2},"wide":{`)
	for i := range 2500 {
		if i > 0 {
			wide.WriteByte(',')
		}
		fmt.Fprintf(&wide, `"w%04d":1`, i)
	}
	wide.WriteString(`},"last":{"d":1,"c":2}}`)
	value, order, err = UnmarshalObject([]byte(wide.String()))
	if err != nil {
		t.Fatal(err)
	}
	if keys, ok := order.KeysOf(value["wide"].(map[string]any)); !ok || len(keys) != 2500 || keys[0] != "w0000" || keys[2499] != "w2499" {
		t.Errorf("wide object: %d keys, %v", len(keys), ok)
	}
	if keys, _ := order.KeysOf(value["first"].(map[string]any)); !reflect.DeepEqual(keys, []string{"b", "a"}) {
		t.Errorf("first = %v", keys)
	}
	if keys, _ := order.KeysOf(value["last"].(map[string]any)); !reflect.DeepEqual(keys, []string{"d", "c"}) {
		t.Errorf("last = %v", keys)
	}
}

// An order is read by several goroutines at once, as the handlers of a fused event read the copies of one master.
func TestObjectOrderIsReadConcurrently(t *testing.T) {
	value, order, err := UnmarshalObject([]byte(`{"a":{"z":1,"y":2},"b":{"x":1,"w":2},"c":{"v":1,"u":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool)
	for range 8 {
		go func() {
			ok := true
			for key, want := range map[string]string{"a": "z", "b": "x", "c": "v"} {
				keys, found := order.KeysOf(value[key].(map[string]any))
				ok = ok && found && keys[0] == want
			}
			done <- ok
		}()
	}
	for range 8 {
		if !<-done {
			t.Error("a concurrent read saw a wrong order")
		}
	}
}
