//go:build darwin

package nativeplatform

import "testing"

// The CGO-free CoreGraphics bridge must resolve CGEventSourceFlagsState and
// call it without crashing; Pi's darwin helper calls the same function.
func TestDarwinModifierBridgeResolvesCoreGraphics(t *testing.T) {
	if loadCGEventSourceFlagsState() == 0 {
		t.Fatal("CGEventSourceFlagsState did not resolve from CoreGraphics")
	}
	for _, name := range []string{"shift", "command", "control", "option"} {
		if modifierMaskForName(name) == 0 {
			t.Fatalf("modifier %q has no mask", name)
		}
		_ = IsModifierPressed(name)
	}
	if modifierMaskForName("hyper") != 0 || IsModifierPressed("hyper") {
		t.Fatal("unknown modifier has a mask")
	}
}
