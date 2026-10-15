//go:build !race

package subprocess

const raceEnabled = false

// raceAllocSlack is the allocations a budget allows above its value: none.
const raceAllocSlack = 0
