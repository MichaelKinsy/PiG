// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package counts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Metrics are the counts of one interval of a replay, by name. A name is "<scope>.<metric>": scope "storage" (what the host
// stores and serves, the same for every build), "native", "tinygo" or "go". Counters add across intervals; gauges (the live set,
// linear memory) take their value at the end of the last one.
type Metrics map[string]int64

// Interval is the counts between one mark of the workload and the next.
type Interval struct {
	Name string
	M    Metrics
}

// gauge reports whether a metric is a size at a moment instead of a count of events.
func gauge(name string) bool {
	_, metric, _ := strings.Cut(name, ".")
	return strings.HasPrefix(metric, "live") || metric == "linearBytes" || metric == "highWaterBytes"
}

// Phases folds the intervals of a workload into the two the gate reports, the ones durable/contract/cli/counts.mjs calls cold
// and warm: "cold" is everything up to the end of the first measured turn (the open and the first turn), "warm" every later
// turn, summed. Warm also carries "<scope>.liveGrowthPerTurn" (liveGrowth): the growth of the live set per turn across the
// warm turns, the figure that is zero when a turn with no live work retains nothing, and "turns", the warm turns.
func Phases(intervals []Interval) map[string]Metrics {
	out := map[string]Metrics{}
	fold := func(name string, from, to int) {
		m := Metrics{}
		for _, iv := range intervals[from:to] {
			for k, v := range iv.M {
				if gauge(k) {
					m[k] = v
				} else {
					m[k] += v
				}
			}
		}
		out[name] = m
	}
	turn0 := -1
	for i, iv := range intervals {
		if iv.Name != "open" {
			turn0 = i
			break
		}
	}
	if turn0 < 0 {
		fold("cold", 0, len(intervals))
		return out
	}
	fold("cold", 0, turn0+1)
	if turn0+1 >= len(intervals) {
		return out
	}
	fold("warm", turn0+1, len(intervals))
	warm := intervals[turn0+1:]
	if len(warm) >= 2 {
		for k := range warm[0].M {
			if !strings.HasSuffix(k, ".liveBytes") {
				continue
			}
			scope, _, _ := strings.Cut(k, ".")
			live := make([]int64, len(warm))
			for i, iv := range warm {
				live[i] = iv.M[k]
			}
			out["warm"][scope+".liveGrowthPerTurn"] = liveGrowth(live)
		}
	}
	out["warm"]["turns"] = int64(len(warm))
	return out
}

// SettleWarm drops the first n warm turns of a workload (the turns after the cold one), so Phases folds only the later ones.
// It serves the gate's live-set window: a live set that rises in a few bounded steps early (a buffer or table reaching its
// working capacity) and then stays flat is not growth, and the slope over the settled turns reads it as zero, while retention
// that every turn adds shows in any window. Fewer warm turns than n leave none.
func SettleWarm(intervals []Interval, n int) []Interval {
	turn0 := slices.IndexFunc(intervals, func(iv Interval) bool { return iv.Name != "open" })
	if n <= 0 || turn0 < 0 {
		return intervals
	}
	from := min(turn0+1+n, len(intervals))
	return slices.Concat(intervals[:turn0+1], intervals[from:])
}

// liveGrowth is the growth per turn of a live set read after a full collection at the end of each of n turns: the slope from
// the lowest reading of the first k = n/2 turns to the lowest of the last k. It is exact for a set that grows by the same bytes
// every turn. A conservative collector's false retention only ever adds bytes to a reading, so it moves the figure only when
// it holds an object through every reading of a window: one object kept live at one reading is not growth. Retention that
// starts after the first reading of the last window does not show: the figure is the growth every turn adds. With two or three
// turns it is the difference of the last and first readings per turn.
func liveGrowth(live []int64) int64 {
	n := len(live)
	k := n / 2
	first := lowest(live[:k])
	last := n - k + lowest(live[n-k:])
	return (live[last] - live[first]) / int64(last-first)
}

// lowest is the position of the first smallest reading.
func lowest(live []int64) int {
	return slices.Index(live, slices.Min(live))
}

// Merge combines two passes over the same recording, interval by interval; a metric both report keeps a's value.
func Merge(a, b []Interval) ([]Interval, error) {
	if len(a) != len(b) {
		return nil, fmt.Errorf("counts: replays of %d and %d intervals cannot be merged", len(a), len(b))
	}
	out := make([]Interval, len(a))
	for i := range a {
		if a[i].Name != b[i].Name {
			return nil, fmt.Errorf("counts: interval %d is %q in one replay and %q in the other", i, a[i].Name, b[i].Name)
		}
		m := Metrics{}
		maps.Copy(m, b[i].M)
		maps.Copy(m, a[i].M)
		out[i] = Interval{a[i].Name, m}
	}
	return out, nil
}

// intervalsOf cuts the recording at its marks. The first interval is "open"; a mark starts the interval of its own name; the
// mark "end" closes the last turn and starts nothing.
func intervalsOf(rec *Recording) []interval {
	var out []interval
	at, name := 0, "open"
	for _, m := range rec.Marks {
		out = append(out, interval{name, at, m.At})
		at, name = m.At, m.Name
	}
	if name != "end" {
		out = append(out, interval{name, at, len(rec.Entries)})
	}
	return out
}

type interval struct {
	name     string
	from, to int
}

// LiveCurve is the native live set (bytes) after each measured turn, in turn order.
func LiveCurve(intervals []Interval) []int64 {
	var live []int64
	for _, iv := range intervals {
		if strings.HasPrefix(iv.Name, "turn ") {
			live = append(live, iv.M["native.liveBytes"])
		}
	}
	return live
}

// SteadyGrowth is the live set's growth per turn over the second half of the turns: what a conversation still keeps once its
// first turns have built whatever a context window needs. It reads the half with liveGrowth, the one estimator of the gate,
// so one object a conservative collector keeps at a single reading does not count. A flat curve is 0; a falling one is negative.
func SteadyGrowth(live []int64) int64 {
	return liveGrowth(live[len(live)/2:])
}
