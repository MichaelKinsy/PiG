//go:build parity

package runner

import (
	"fmt"
	"testing"
)

// Every scenario lands in exactly one shard, whatever the shard count.
func TestFilterByShardPartitionsTheScenarios(t *testing.T) {
	var scenarios []*Scenario
	for i := range 200 {
		scenarios = append(scenarios, &Scenario{Name: fmt.Sprintf("%02d-scenario-%d", i%90, i)})
	}
	for _, shards := range []int{1, 2, 3} {
		seen := map[string]int{}
		for shard := 1; shard <= shards; shard++ {
			for _, sc := range FilterByShard(scenarios, shard, shards) {
				seen[sc.Name]++
			}
		}
		for _, sc := range scenarios {
			if seen[sc.Name] != 1 {
				t.Fatalf("%d shards: %s ran %d times", shards, sc.Name, seen[sc.Name])
			}
		}
	}
	for _, bad := range []string{"0/2", "3/2", "1", "a/b", "1/0"} {
		if _, _, err := parseShard(bad); err == nil {
			t.Errorf("parseShard(%q) accepted", bad)
		}
	}
}
