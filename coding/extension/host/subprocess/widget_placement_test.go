package subprocess

import (
	"slices"
	"testing"
)

// Pi removes the old widget before appending its replacement.
func TestWidgetReplacementResetsPlacementAndOrder(t *testing.T) {
	for _, method := range []string{"host-call", "sdk-push"} {
		t.Run(method, func(t *testing.T) {
			bridge := newTestBridge(&mockUIContext{})
			for _, args := range []string{
				`{"key":"first","content":["first"],"options":{"placement":"belowEditor"}}`,
				`{"key":"second","content":["second"]}`,
			} {
				if _, err := call(bridge, "ui.setWidget", args); err != nil {
					t.Fatal(err)
				}
			}
			_, secondOrder := bridge.GetWidget("test-ext", "second").WidgetLayout()
			if method == "sdk-push" {
				bridge.HandleWidgetPush("test-ext", nil, &WidgetPushPayload{Key: "first", Lines: []string{"replaced"}})
			} else if _, err := call(bridge, "ui.setWidget", `{"key":"first","content":["replaced"]}`); err != nil {
				t.Fatal(err)
			}
			placement, order := bridge.GetWidget("test-ext", "first").WidgetLayout()
			if placement == "belowEditor" || order <= secondOrder {
				t.Fatalf("replacement retained old layout: %q/%d, second order %d", placement, order, secondOrder)
			}
		})
	}
}

func TestWidgetPlacementSurvivesFrameUpdatesAndClears(t *testing.T) {
	bridge := newTestBridge(&mockUIContext{})
	for _, args := range []string{
		`{"key":"first","content":["first"]}`,
		`{"key":"second","content":["second"],"width":40,"options":{"placement":"belowEditor"}}`,
	} {
		if _, err := call(bridge, "ui.setWidget", args); err != nil {
			t.Fatal(err)
		}
	}
	first := bridge.GetWidget("test-ext", "first")
	second := bridge.GetWidget("test-ext", "second")
	placement, firstOrder := first.WidgetLayout()
	if placement != "aboveEditor" {
		t.Fatalf("default placement = %q", placement)
	}
	placement, secondOrder := second.WidgetLayout()
	if placement != "belowEditor" || secondOrder <= firstOrder {
		t.Fatalf("second layout = %q/%d, first order = %d", placement, secondOrder, firstOrder)
	}
	if got := second.Render(41); len(got) != 0 {
		t.Fatalf("wrong-width frame painted: %q", got)
	}
	bridge.HandleWidgetPush("test-ext", nil, &WidgetPushPayload{Key: "second", Lines: []string{"updated"}, Width: 40})
	if placement, order := second.WidgetLayout(); placement != "belowEditor" || order != secondOrder {
		t.Fatalf("frame update changed placement/order: %q/%d", placement, order)
	}
	if got := second.Render(40); !slices.Equal(got, []string{"updated"}) {
		t.Fatalf("frame = %q", got)
	}
	if _, err := call(bridge, "ui.setWidget", `{"key":"second","content":null}`); err != nil {
		t.Fatal(err)
	}
	if bridge.GetWidget("test-ext", "second") != nil {
		t.Fatal("cleared widget retained")
	}
	bridge.ClearExtension("test-ext")
	if len(bridge.AllWidgets()) != 0 {
		t.Fatal("extension cleanup retained widgets")
	}
}
