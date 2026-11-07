package boltruntime

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRewriteFuzzHandCases(t *testing.T) {
	path := os.Getenv("PIG_AOT_FUZZ_HAND_OUT")
	if path == "" {
		t.Skip("set PIG_AOT_FUZZ_HAND_OUT to write the hand cases")
	}
	type item struct {
		Kind string `json:"kind"`
		File string `json:"file"`
		Src  string `json:"src"`
		Exp  string `json:"exp"`
		Tags []string
	}
	var items []item
	for _, c := range scannerCases {
		want := c.want
		if want == same {
			want = c.in
		}
		items = append(items, item{Kind: "hand", File: c.file, Src: c.in, Exp: want, Tags: []string{c.name}})
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
