// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"encoding/json"
	"math"
	"testing"
)

// A retention of Infinity keeps for good and must survive the JSON of the open payload, which cannot carry it.
func TestAnInfiniteRetentionIsCarriedAsAFiniteNumberThatKeepsForGood(t *testing.T) {
	encoded, err := json.Marshal(map[string]any{"documentRetentionMs": retentionJSON(math.Inf(1)), "contextRetentionMs": retentionJSON(600000)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]float64
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["documentRetentionMs"] != math.MaxFloat64 || decoded["contextRetentionMs"] != 600000 {
		t.Fatalf("decoded %v", decoded)
	}
}
