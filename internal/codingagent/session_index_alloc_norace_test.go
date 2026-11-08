//go:build !race

package codingagent

import "testing"

// A line whose parentId is the id of the line before it shares that string, which is one allocation fewer: its bytes.
func TestIndexProbeSharesThePreviousID(t *testing.T) {
	line := []byte(`{"type":"message","id":"b2","parentId":"a1","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"x"}],"timestamp":12}}`)
	previous := string([]byte("a1"))
	shared := testing.AllocsPerRun(50, func() { _, _ = walkIndexProbeAfter(line, previous) })
	own := testing.AllocsPerRun(50, func() { _, _ = walkIndexProbeAfter(line, "") })
	if shared >= own {
		t.Errorf("allocs with the previous id = %v, without = %v; want fewer", shared, own)
	}
}
