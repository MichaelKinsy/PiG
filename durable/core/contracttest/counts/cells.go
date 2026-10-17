// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package counts

import "strconv"

// implOf names the builds as durable/contract/impls.toml does.
var implOf = map[string]string{"native": "native", "tinygo": "wasm-tinygo", "go": "wasm-go"}

// exported are the counts this package adds to the cells of durable/contract/cli/counts.mjs (which counts rows, crossings,
// the heap, linear memory and the allocator's own totals itself), by metric. The calls of the allocator that replay counts by
// name are a different quantity from the runtime's own totals (requested bytes, not blocks), so they have names of their own.
var exported = map[string]string{
	"fuel": "instructions", "liveGrowthPerTurn": "liveGrowthPerTurn", "allocCalls": "allocCalls", "allocRequestedBytes": "allocRequestedBytes", "gcCycles": "gcCycles",
}

// Cells turns the phases of a size into the flattened cells of the contract's count baseline: "impl/size" to "cold.metric" and
// "warm.metric". Only the counts that replay measures and the contract's own counters do not are exported; "turns" and the
// per-interval gauges stay in the report.
func Cells(size int, phases map[string]Metrics) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	for phase, metrics := range phases {
		for name, v := range metrics {
			scope, metric := split(name)
			want, ok := exported[metric]
			impl := implOf[scope]
			if !ok || impl == "" {
				continue
			}
			cell := impl + "/" + strconv.Itoa(size)
			if out[cell] == nil {
				out[cell] = map[string]int64{}
			}
			out[cell][phase+"."+want] = v
		}
	}
	return out
}

func split(name string) (scope, metric string) {
	for i := range len(name) {
		if name[i] == '.' {
			return name[:i], name[i+1:]
		}
	}
	return "", name
}
