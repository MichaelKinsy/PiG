// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// Pi's synchronous getter cannot interleave an applyOverrides call between fields.
func TestCompactionSettingsReadOneSnapshot(t *testing.T) {
	first := Settings{Compaction: &CompactionSettingsJSON{Enabled: new(true), ReserveTokens: new(1.), KeepRecentTokens: new(1.)}}
	second := Settings{Compaction: &CompactionSettingsJSON{Enabled: new(false), ReserveTokens: new(2.), KeepRecentTokens: new(2.)}}
	sm := &SettingsManager{merged: first}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() {
		for ctx.Err() == nil {
			sm.ApplyOverrides(first)
			sm.ApplyOverrides(second)
		}
	})
	defer func() { cancel(); wg.Wait() }()
	for range 10000 {
		got, err := sm.GetCompactionSettings()
		if err != nil {
			t.Fatal(err)
		}
		want := CompactionConfig{false, 2, 2}
		if got.Enabled {
			want = CompactionConfig{true, 1, 1}
		}
		if got != want {
			t.Fatalf("mixed settings snapshot=%+v; want %+v", got, want)
		}
	}
}

func BenchmarkCompactionSettingsModelOverrides(b *testing.B) {
	overrides := make(map[string]CompactionModelOverride, 1000)
	for i := range 1000 {
		overrides[fmt.Sprintf("provider/model-%d", i)] = CompactionModelOverride{ReserveTokens: new(float64(i)), KeepRecentTokens: new(10000.)}
	}
	sm := &SettingsManager{merged: Settings{Compaction: &CompactionSettingsJSON{ModelOverrides: overrides}}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sm.GetModelCompactionSettings("provider", "model-999"); err != nil {
			b.Fatal(err)
		}
	}
}
