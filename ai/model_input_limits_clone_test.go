package ai

import (
	"reflect"
	"testing"
)

var inputLimitsSink *ModelInputLimits

// Clone copies the limits, the image limits and the resize options into objects of their own, in one allocation.
func TestModelInputLimitsCloneIsADeepCopyInOneAllocation(t *testing.T) {
	cases := map[string]*ModelInputLimits{
		"request only":      {MaxRequestBytes: 1 << 20},
		"images":            {MaxRequestBytes: 7, Images: &ModelImageInputLimits{MaxPerMessage: 2, MaxPerRequest: 9}},
		"images and resize": {Images: &ModelImageInputLimits{MaxPerMessage: 3, Resize: &ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 1500, MaxBytes: 5 << 20, JPEGQuality: 80}}},
	}
	for name, limits := range cases {
		original := *limits
		var images ModelImageInputLimits
		var resize ModelImageResizeOptions
		if limits.Images != nil {
			images = *limits.Images
			if limits.Images.Resize != nil {
				resize = *limits.Images.Resize
			}
		}
		clone := limits.Clone()
		if clone == limits || !reflect.DeepEqual(clone, limits) {
			t.Fatalf("%s: Clone() = %+v, want an equal copy of %+v", name, clone, limits)
		}
		if limits.Images != nil {
			if clone.Images == limits.Images || (limits.Images.Resize != nil && clone.Images.Resize == limits.Images.Resize) {
				t.Fatalf("%s: the copy shares a nested object with the source", name)
			}
			clone.Images.MaxPerMessage++
			if clone.Images.Resize != nil {
				clone.Images.Resize.MaxWidth++
			}
		}
		clone.MaxRequestBytes++
		if *limits != original || (limits.Images != nil && *limits.Images != images) || (limits.Images != nil && limits.Images.Resize != nil && *limits.Images.Resize != resize) {
			t.Fatalf("%s: writing the copy changed the source", name)
		}
		if allocs := testing.AllocsPerRun(20, func() { inputLimitsSink = limits.Clone() }); allocs != 1 {
			t.Fatalf("%s: Clone allocated %.0f times, want 1", name, allocs)
		}
	}
	if (*ModelInputLimits)(nil).Clone() != nil {
		t.Fatal("Clone of nil limits is not nil")
	}
}
