package wordsegmenter

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestICUSoutheastAsianSegments(t *testing.T) {
	data, err := os.ReadFile("testdata/sea-icu78.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Node, ICU, Pi string
		Cases         []struct {
			Script, Text string
			Segments     []SegmentData
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 || fixture.Node != "26.7.0" || fixture.ICU != "78.3" || fixture.Pi != "0.87.1" {
		t.Fatal("expected nonempty pinned Node 26.7.0 / ICU 78.3 / Pi 0.87.1 corpus")
	}
	for i, tc := range fixture.Cases {
		got := slices.Collect(Segments(tc.Text))
		for j := range got {
			got[j].Input = ""
		}
		if !slices.Equal(got, tc.Segments) {
			t.Errorf("case %d (%s) %q: got %+v want %+v", i, tc.Script, tc.Text, got, tc.Segments)
		}
	}
}
