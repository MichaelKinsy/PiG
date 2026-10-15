package extension

import (
	"reflect"
	"testing"
)

// Pi core/extensions/types.ts WidgetPlacement is exactly "aboveEditor" | "belowEditor"; the host layers and the SDK wire use these literals.
func TestWidgetPlacementLiteralsMatchUpstream(t *testing.T) {
	if WidgetPlacementAboveEditor != "aboveEditor" || WidgetPlacementBelowEditor != "belowEditor" {
		t.Fatalf("placements %q %q", WidgetPlacementAboveEditor, WidgetPlacementBelowEditor)
	}
}

// Pi powershell.ts PowerShellToolInput = BashToolInput: a powershell tool_call event carries the same command/timeout input as bash.
func TestPowerShellToolInputIsTheBashInputShape(t *testing.T) {
	timeout := 5.0
	// PowerShellToolInput is an alias, so the bash input is the powershell event's input with no conversion.
	if reflect.TypeFor[PowerShellToolInput]() != reflect.TypeFor[BashToolInput]() {
		t.Fatalf("PowerShellToolInput is %v, want BashToolInput", reflect.TypeFor[PowerShellToolInput]())
	}
	input := BashToolInput{Command: "Get-Date", Timeout: &timeout}
	asPowerShell := PowerShellToolInput(input)
	if asPowerShell.Command != "Get-Date" || asPowerShell.Timeout == nil || *asPowerShell.Timeout != 5 {
		t.Fatalf("input %v", asPowerShell)
	}
}
