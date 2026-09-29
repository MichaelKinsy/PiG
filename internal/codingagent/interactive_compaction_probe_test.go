// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestCompactionBillingRenderProbe(t *testing.T) {
	old := tui.ActiveTheme().Name
	tui.SetTheme("dark")
	defer tui.SetTheme(old)
	dir := t.TempDir()
	settings := NewSettingsManager(dir, dir)
	if err := settings.SetShowCacheMissNotices(true); err != nil {
		t.Fatal(err)
	}
	mode := &InteractiveMode{opts: InteractiveOptions{SettingsManager: settings}, chatContainer: tui.NewContainer()}
	usage := compactionUIUsage(.125)
	mode.addCompactionCostNotice("compaction", usage)
	mode.addCompactionCostNotice("branch_summary", usage)
	data, err := json.Marshal(mode.chatContainer.Render(120))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("COMPACTION_BILLING %s\n", data)
}

func BenchmarkCompactionBillingRows(b *testing.B) {
	dir := b.TempDir()
	settings := NewSettingsManager(dir, dir)
	if err := settings.SetShowCacheMissNotices(true); err != nil {
		b.Fatal(err)
	}
	mode := &InteractiveMode{opts: InteractiveOptions{SettingsManager: settings}, chatContainer: tui.NewContainer()}
	usage := compactionUIUsage(.125)
	b.ReportAllocs()
	for b.Loop() {
		mode.chatContainer.Clear()
		mode.addCompactionCostNotice("compaction", usage)
		mode.addCompactionCostNotice("branch_summary", usage)
		if len(mode.chatContainer.Render(120)) == 0 {
			b.Fatal("missing cost rows")
		}
	}
}
