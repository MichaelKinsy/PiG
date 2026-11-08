package json

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
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
	carried := order.Copies()
	carried.Carry(x, copied)
	carried.Carry(map[string]any{"q": 1, "r": 2}, map[string]any{"q": 1, "r": 2})
	carried.Carry(nil, map[string]any{})
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

// countingOrder is a KeyOrder that counts the questions it is asked.
type countingOrder struct{ asked int }

func (c *countingOrder) Keys(uintptr) ([]string, bool) { c.asked++; return nil, false }

// A map of one member has no order to choose, so the encoder does not ask for it: an unchanged-context reply, which is one such map, reads nothing of the
// event.
func TestMarshalJSOrderedAsksOnlyForMapsOfTwoMembersOrMore(t *testing.T) {
	var order countingOrder
	if _, err := MarshalJSOrdered(map[string]any{"_pigContextUnchanged": true}, &order); err != nil {
		t.Fatal(err)
	}
	if order.asked != 0 {
		t.Errorf("a map of one member asked %d times", order.asked)
	}
	// With the order of a decode: a reply of one member neither builds the filter nor sorts the objects.
	_, decoded, err := UnmarshalObject([]byte(`{"a":{"b":1,"c":2},"d":{"e":1,"f":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MarshalJSOrdered(map[string]any{"_pigContextUnchanged": true}, decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.sorted != nil || decoded.filter != nil {
		t.Error("a reply of one member read the order of the decode")
	}
	if _, err := MarshalJSOrdered(map[string]any{"a": map[string]any{"b": 1, "c": 2}, "d": []any{map[string]any{"e": 1}}}, &order); err != nil {
		t.Fatal(err)
	}
	if order.asked != 2 {
		t.Errorf("a map of two members and one nested map of two asked %d times, want 2", order.asked)
	}
}

// A lookup of a map that is not one of the objects does not sort them, and a lookup of one that is finds it among thousands.
func TestObjectOrderLookupOfAnotherMapDoesNotSort(t *testing.T) {
	var text strings.Builder
	text.WriteString(`{"list":[`)
	for i := range 2000 {
		if i > 0 {
			text.WriteByte(',')
		}
		text.WriteString(`{"b":1,"a":2}`)
	}
	text.WriteString(`]}`)
	value, order, err := UnmarshalObject([]byte(text.String()))
	if err != nil {
		t.Fatal(err)
	}
	// An address no object has that the filter does not pass: the filter's answer is "not here" without a sort. (A map the filter passes by chance is sorted
	// for, and found missing; its address cannot be chosen, so the test chooses an address.)
	order.Keys(8)
	var other uintptr
	for other = 16; order.mayHold(other); other += 8 {
		if other > 1<<24 {
			t.Fatal("the filter passes every address")
		}
	}
	if _, ok := order.Keys(other); ok {
		t.Fatal("an address that is not an object has an order")
	}
	if order.sorted != nil {
		t.Error("a lookup of an address the filter rejects sorted the objects")
	}
	if keys, ok := order.KeysOf(value["list"].([]any)[1234].(map[string]any)); !ok || keys[0] != "b" {
		t.Errorf("object 1234 = %v, %v", keys, ok)
	}
	if order.sorted == nil {
		t.Error("a lookup of an object did not sort")
	}
}

// sortOrderEntries sorts by pointer for every input shape a quicksort can be hurt by, including the heapsort fallback.
func TestSortOrderEntriesSortsByPointer(t *testing.T) {
	shapes := map[string]func(i, n int) uintptr{
		"ascending":  func(i, n int) uintptr { return uintptr(i) * 64 },
		"descending": func(i, n int) uintptr { return uintptr(n-i) * 64 },
		"organ pipe": func(i, n int) uintptr { return uintptr(min(i, n-i)) * 64 },
		"shuffled":   func(i, n int) uintptr { return uintptr(i*7919%n) * 64 },
		"sawtooth":   func(i, n int) uintptr { return uintptr(i%17)*1000 + uintptr(i)*8 },
	}
	for name, pointerAt := range shapes {
		for _, n := range []int{0, 1, 2, 12, 13, 100, 1000, 20011} {
			s := make([]orderEntry, n)
			for i := range s {
				s[i].pointer = pointerAt(i, n)
			}
			want := make([]uintptr, n)
			for i := range s {
				want[i] = s[i].pointer
			}
			slices.Sort(want)
			sortOrderEntries(s)
			for i := range s {
				if s[i].pointer != want[i] {
					t.Fatalf("%s, n=%d: entry %d has pointer %d, want %d", name, n, i, s[i].pointer, want[i])
				}
			}
		}
	}
	// The heapsort the quicksort falls back to sorts on its own.
	s := make([]orderEntry, 5000)
	for i := range s {
		s[i].pointer = uintptr(i * 7919 % 5000)
	}
	heapOrderEntries(s)
	for i := 1; i < len(s); i++ {
		if s[i-1].pointer > s[i].pointer {
			t.Fatalf("heapOrderEntries: entry %d is out of order", i)
		}
	}
}

// BenchmarkSortOrderEntries compares the local sort with slices.SortFunc, the copy of the standard library's sort it replaced, on the addresses of n objects:
// "shuffled" in a random order, "runs" as a decode leaves them (ascending within each 64-object span, the spans in a random order).
func BenchmarkSortOrderEntries(b *testing.B) {
	for _, n := range []int{100, 2000, 50000} {
		for _, shape := range []string{"shuffled", "runs"} {
			source := make([]orderEntry, n)
			for i := range source {
				p := uintptr(i*7919%n) * 48
				if shape == "runs" {
					p = uintptr((i/64)*7919%(n/64+1))*64*48 + uintptr(i%64)*48
				}
				source[i].pointer = p
			}
			work := make([]orderEntry, n)
			b.Run(fmt.Sprintf("local/%s/%d", shape, n), func(b *testing.B) {
				for range b.N {
					copy(work, source)
					sortOrderEntries(work)
				}
			})
			b.Run(fmt.Sprintf("slices/%s/%d", shape, n), func(b *testing.B) {
				for range b.N {
					copy(work, source)
					slices.SortFunc(work, func(a, b orderEntry) int { return cmp.Compare(a.pointer, b.pointer) })
				}
			})
		}
	}
}
