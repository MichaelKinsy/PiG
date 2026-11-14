package json

import "testing"

type fieldOrderDeclared struct {
	B int
	A int `json:"a"`
	C int `json:"B2"`
}

type fieldOrderInner struct{ A, B int }

type fieldOrderShadow struct {
	B string
	fieldOrderInner
}

type fieldOrderPlain struct{ N int }

type fieldOrderTagged struct {
	A int `json:"N"`
}

type fieldOrderTagWins struct {
	fieldOrderPlain
	fieldOrderTagged
}

type fieldOrderLeaf struct {
	X int
	Y string `json:"y"`
}

type fieldOrderViaA struct{ fieldOrderLeaf }

type fieldOrderViaB struct{ fieldOrderLeaf }

// fieldOrderTwice reaches fieldOrderLeaf twice at one depth: typeFields lists X and y twice, as equal copies, and drops both.
type fieldOrderTwice struct {
	fieldOrderViaA
	fieldOrderViaB
	Z int
}

type fieldOrderWide struct{ P, O, N, M, L, K, J, I, H, G, F, E, D, C, B, A int }

// typeFields sorts a struct's fields by name to find the dominant field of each name, then by index for the output. The
// members come out in declaration order, a shallower field hides a deeper one, a tagged field wins over an untagged one
// of its depth, and a field reached twice at one depth is left out.
func TestStructMemberOrderIsPinned(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"declaration order, not name order", fieldOrderDeclared{B: 1, A: 2, C: 3}, `{"B":1,"a":2,"B2":3}`},
		{"the shallower field hides the embedded one", fieldOrderShadow{B: "x", fieldOrderInner: fieldOrderInner{A: 1, B: 2}}, `{"B":"x","A":1}`},
		{"the tagged field wins at one depth", fieldOrderTagWins{fieldOrderPlain{N: 1}, fieldOrderTagged{A: 2}}, `{"N":2}`},
		{"a field reached twice at one depth is dropped (equal keys in the sort)", fieldOrderTwice{Z: 3}, `{"Z":3}`},
		{"sixteen fields declared against name order", fieldOrderWide{}, `{"P":0,"O":0,"N":0,"M":0,"L":0,"K":0,"J":0,"I":0,"H":0,"G":0,"F":0,"E":0,"D":0,"C":0,"B":0,"A":0}`},
	} {
		got, err := Marshal(test.value)
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if string(got) != test.want {
			t.Errorf("%s: Marshal = %s, want %s", test.name, got, test.want)
		}
	}
}
