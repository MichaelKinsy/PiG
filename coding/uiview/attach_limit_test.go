package uiview_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestAttachBound: a registry holds 8 attachments, of the one-client form, of Go clients that take deliveries and of
// any other kind together; the ninth is refused with code limit, and a detach makes room again.
func TestAttachBound(t *testing.T) {
	r := uiview.New(uiview.Options{})
	ab := uiview.ClientAbilities{Slots: []uiview.Slot{uiview.SlotPanel}, Nodes: []string{"text"}, Limits: uiview.Limits{Nodes: 8, Depth: 4, SnapshotBytes: 1024, TextBytes: 256, Mounts: 4, IntentArgBytes: 16}}
	if err := r.SetAbilities(&ab); err != nil {
		t.Fatal(err)
	}
	var held []*uiview.Attachment
	for i := 2; i <= 8; i++ {
		a, _, err := r.Attach(ab, func(uiview.Delivery) bool { return true })
		if err != nil {
			t.Fatalf("attachment %d: %v", i, err)
		}
		held = append(held, a)
	}
	if _, _, err := r.Attach(ab, nil); err == nil || err.Code != uiview.CodeLimit {
		t.Fatalf("the ninth attachment = %v, want code limit", err)
	}
	held[0].Detach()
	a, _, err := r.Attach(ab, nil)
	if err != nil {
		t.Fatalf("an attachment after a detach: %v", err)
	}
	if _, _, err := r.Attach(ab, nil); err == nil || err.Code != uiview.CodeLimit {
		t.Fatalf("the ninth attachment = %v, want code limit", err)
	}
	a.Detach()
	if err := r.SetAbilities(nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := r.Attach(ab, nil); err != nil {
			t.Fatalf("an attachment after the one-client form detached: %v", err)
		}
	}
}
