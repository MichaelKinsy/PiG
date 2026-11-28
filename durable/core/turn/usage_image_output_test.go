// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func TestAddUsageImageOutput(t *testing.T) {
	base := jv.NewObject()
	base.Set("input", float64(10))
	base.Set("output", float64(20))
	base.Set("cacheRead", float64(0))
	base.Set("cacheWrite", float64(0))
	base.Set("totalTokens", float64(30))
	cost := jv.NewObject()
	cost.Set("input", float64(0.1))
	cost.Set("output", float64(0.2))
	cost.Set("cacheRead", float64(0))
	cost.Set("cacheWrite", float64(0))
	cost.Set("total", float64(0.3))
	base.Set("cost", cost)

	withImage := jv.Clone(base).(*jv.Object)
	withImage.Set("imageOutput", float64(5))

	// Adding usage without imageOutput leaves it absent.
	total := jv.Clone(base).(*jv.Object)
	AddUsage(total, base)
	if _, has := total.Get("imageOutput"); has {
		t.Fatal("imageOutput should remain absent when neither side reports it")
	}

	// Adding usage with imageOutput initializes and accumulates it.
	AddUsage(total, withImage)
	if got, _ := total.Get("imageOutput"); got != float64(5) {
		t.Fatalf("imageOutput = %v, want 5", got)
	}
	AddUsage(total, withImage)
	if got, _ := total.Get("imageOutput"); got != float64(10) {
		t.Fatalf("imageOutput = %v, want 10", got)
	}

	// Adding usage without imageOutput preserves accumulated value.
	AddUsage(total, base)
	if got, _ := total.Get("imageOutput"); got != float64(10) {
		t.Fatalf("imageOutput = %v, want 10", got)
	}
}

func TestAddUsageDraftImageOutput(t *testing.T) {
	base := jv.NewObject()
	base.Set("input", float64(1))
	base.Set("output", float64(2))
	base.Set("cacheRead", float64(0))
	base.Set("cacheWrite", float64(0))
	base.Set("totalTokens", float64(3))
	cost := jv.NewObject()
	cost.Set("input", float64(0))
	cost.Set("output", float64(0))
	cost.Set("cacheRead", float64(0))
	cost.Set("cacheWrite", float64(0))
	cost.Set("total", float64(0))
	base.Set("cost", cost)

	usage := jv.Clone(base).(*jv.Object)
	usage.Set("imageOutput", float64(7))

	tracker := delta.Track(base)
	change := tracker.BeginChange()
	defer change.Abort()
	if err := addUsageDraft(change.State(), usage); err != nil {
		t.Fatal(err)
	}
	if err := addUsageDraft(change.State(), usage); err != nil {
		t.Fatal(err)
	}
	if got, _ := change.State().Get("imageOutput").(float64); got != 14 {
		t.Fatalf("draft imageOutput = %v, want 14", got)
	}
}
