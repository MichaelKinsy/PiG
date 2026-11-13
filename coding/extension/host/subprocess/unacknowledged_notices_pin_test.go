package subprocess

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// unacknowledgedNoticesWithFmt is unacknowledgedNotices as it was written with slices.SortFunc and fmt.Sprintf: the reference for the lines' text and order.
func unacknowledgedNoticesWithFmt(h *Host, unacknowledged []unacknowledgedGeneration) []string {
	if len(unacknowledged) == 0 {
		return nil
	}
	slices.SortFunc(unacknowledged, func(a, b unacknowledgedGeneration) int { return cmp.Compare(a.me.config.Name, b.me.config.Name) })
	var order []any
	processes := make(map[any][]unacknowledgedGeneration)
	for _, generation := range unacknowledged {
		process := generation.me.runtimeProcess()
		if _, seen := processes[process]; !seen {
			order = append(order, process)
		}
		processes[process] = append(processes[process], generation)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	outcome := func(generations ...unacknowledgedGeneration) string {
		for _, generation := range generations {
			if current := h.exts[generation.me.config.Name]; current != nil && current != generation.me {
				return "restarted"
			}
		}
		return "stopped"
	}
	var lines []string
	for _, process := range order {
		generations := processes[process]
		named := generations
		if len(generations) > 1 {
			named = slices.DeleteFunc(slices.Clone(generations), func(generation unacknowledgedGeneration) bool { return generation.lostRequests == 0 })
		}
		for _, generation := range named {
			lines = append(lines, fmt.Sprintf("extension %q did not acknowledge the reload within %s and was %s", generation.me.config.Name, generation.deadline, outcome(generation)))
		}
		if len(named) == 0 {
			names := make([]string, len(generations))
			for i, generation := range generations {
				names[i] = fmt.Sprintf("%q", generation.me.config.Name)
			}
			lines = append(lines, fmt.Sprintf("the runtime that extensions %s share did not acknowledge the reload within %s and was %s", strings.Join(names, ", "), generations[0].deadline, outcome(generations...)))
		}
	}
	return lines
}

// The text of the lines for names that need quoting: a quote, a backslash, a control character, a non-ASCII rune, a rune that does not print, an invalid UTF-8
// byte, and the empty name; and their order when two generations have one name (the order they came in, which only their deadlines show).
func TestUnacknowledgedNoticesTextAndOrder(t *testing.T) {
	h := NewHost(t.TempDir())
	shared := &processShare{}
	generation := func(name string, share *processShare, deadline time.Duration, lost int) unacknowledgedGeneration {
		return unacknowledgedGeneration{me: &managedExt{config: ExtConfig{Name: name}, share: share}, deadline: deadline, lostRequests: lost}
	}
	ended := []unacknowledgedGeneration{
		generation("twin", nil, 2*time.Second, 0),
		generation("z\xffbyte", shared, 1500*time.Millisecond, 0),
		generation(`say "hi"`, nil, time.Second, 0),
		generation("twin", nil, time.Second, 0),
		generation(`back\slash`, nil, time.Minute, 1),
		generation("caf\u00e9 \u4e16\u754c", nil, 90*time.Second, 0),
		generation("tab\there\n", nil, 0, 0),
		generation("zero\u200bwidth", shared, 1500*time.Millisecond, 0),
		generation("", nil, 250*time.Millisecond, 0),
		generation("twin", nil, 3*time.Second, 0),
	}
	// The reload loaded "twin" again and left the others stopped.
	h.exts["twin"] = &managedExt{config: ExtConfig{Name: "twin"}}
	want := []string{
		`extension "" did not acknowledge the reload within 250ms and was stopped`,
		`extension "back\\slash" did not acknowledge the reload within 1m0s and was stopped`,
		`extension "café 世界" did not acknowledge the reload within 1m30s and was stopped`,
		`extension "say \"hi\"" did not acknowledge the reload within 1s and was stopped`,
		`extension "tab\there\n" did not acknowledge the reload within 0s and was stopped`,
		`extension "twin" did not acknowledge the reload within 2s and was restarted`,
		`extension "twin" did not acknowledge the reload within 1s and was restarted`,
		`extension "twin" did not acknowledge the reload within 3s and was restarted`,
		`the runtime that extensions "zero\u200bwidth", "z\xffbyte" share did not acknowledge the reload within 1.5s and was stopped`,
	}
	if got := h.unacknowledgedNotices(slices.Clone(ended)); !slices.Equal(got, want) {
		t.Errorf("the reload's lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := unacknowledgedNoticesWithFmt(h, slices.Clone(ended)); !slices.Equal(got, want) {
		t.Errorf("the lines of the fmt form:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Over tables of generations with repeated names, shared processes and every outcome, below and above the length at which the sort stops being an insertion
// sort, the lines are those of the form written with slices.SortFunc and fmt.Sprintf.
func TestUnacknowledgedNoticesMatchTheFmtForm(t *testing.T) {
	names := []string{"a", "b", "c", "d", `q"uote`, `b\s`, "\xfe", "\u00e9", "caf\u00e9", "line\nbreak", "\u2028", "\U0001f600", "", "ext-13", "ext-14", "ext-15", "ext-16", "ext-17"}
	deadlines := []time.Duration{time.Second, 1500 * time.Millisecond, 30 * time.Second, 2 * time.Minute}
	random := rand.New(rand.NewPCG(7, 11))
	for table := range 400 {
		h := NewHost(t.TempDir())
		shares := []*processShare{nil, {}, {}, {}}
		ended := make([]unacknowledgedGeneration, random.IntN(41))
		for i := range ended {
			name := names[random.IntN(len(names))]
			if table%4 == 0 {
				// Mostly distinct names, as a reload has them.
				name = fmt.Sprintf("%s-%02d", name, i)
			}
			me := &managedExt{config: ExtConfig{Name: name}, share: shares[random.IntN(len(shares))]}
			ended[i] = unacknowledgedGeneration{me: me, deadline: deadlines[random.IntN(len(deadlines))], lostRequests: random.IntN(3)}
			switch random.IntN(3) {
			case 0:
				h.exts[name] = &managedExt{config: me.config}
			case 1:
				h.exts[name] = me
			}
		}
		want := unacknowledgedNoticesWithFmt(h, slices.Clone(ended))
		got := h.unacknowledgedNotices(slices.Clone(ended))
		if !slices.Equal(got, want) {
			t.Fatalf("table %d (%d generations): the lines:\n%s\nwant:\n%s", table, len(ended), strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

func BenchmarkUnacknowledgedNotices(b *testing.B) {
	h := NewHost(b.TempDir())
	shared, quiet := &processShare{}, &processShare{}
	generation := func(name string, share *processShare, lost int) unacknowledgedGeneration {
		return unacknowledgedGeneration{me: &managedExt{config: ExtConfig{Name: name}, share: share}, deadline: defaultHeartbeatTimeout, lostRequests: lost}
	}
	ended := []unacknowledgedGeneration{
		generation("timer-b", quiet, 0),
		generation("solo", nil, 0),
		generation("neighbour", shared, 0),
		generation("gone", nil, 1),
		generation("busy", shared, 1),
		generation("timer-a", quiet, 0),
	}
	for _, one := range ended {
		if one.me.config.Name != "gone" {
			h.exts[one.me.config.Name] = &managedExt{config: one.me.config}
		}
	}
	work := make([]unacknowledgedGeneration, len(ended))
	b.ReportAllocs()
	for b.Loop() {
		copy(work, ended)
		if lines := h.unacknowledgedNotices(work); len(lines) != 4 {
			b.Fatalf("%d lines", len(lines))
		}
	}
}
