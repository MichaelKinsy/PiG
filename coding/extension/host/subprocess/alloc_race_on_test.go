//go:build race

package subprocess

const raceEnabled = true

// raceAllocSlack is the allocations a budget allows above its value: the race detector makes sync.Pool drop items at random.
const raceAllocSlack = 8
