//go:build parity

package runner

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// parseShard reads "N/M": shard N (1-based) of M.
func parseShard(value string) (shard, shards int, err error) {
	n, m, ok := strings.Cut(value, "/")
	shard, errN := strconv.Atoi(n)
	shards, errM := strconv.Atoi(m)
	if !ok || errN != nil || errM != nil || shards < 1 || shard < 1 || shard > shards {
		return 0, 0, fmt.Errorf("-pig-parity.shard %q: want N/M with 1 <= N <= M", value)
	}
	return shard, shards, nil
}

// FilterByShard keeps the scenarios whose name's FNV-1a hash falls in shard (1-based) of shards, the partition
// automation/ci/test-shard-pattern.sh uses for Go test names, so every scenario runs in exactly one shard.
func FilterByShard(scenarios []*Scenario, shard, shards int) []*Scenario {
	var kept []*Scenario
	for _, sc := range scenarios {
		h := fnv.New32a()
		_, _ = h.Write([]byte(sc.Name))
		if int(h.Sum32()%uint32(shards))+1 == shard {
			kept = append(kept, sc)
		}
	}
	return kept
}
