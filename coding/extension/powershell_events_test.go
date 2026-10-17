package extension

import (
	"encoding/json"
	"testing"
)

// Upstream 0.87.1 types.ts adds PowerShellToolResultEvent to the ToolResultEvent union; its details are the bash shape (powershell.ts PowerShellToolDetails = BashToolDetails). A powershell result must decode to its own variant, not the custom-tool fallback.
func TestUnmarshalPowerShellToolResultEventDispatchByName(t *testing.T) {
	result, err := UnmarshalToolResultEvent([]byte(`{"type":"tool_result","toolCallId":"ps-1","toolName":"powershell","input":{"command":"Get-ChildItem"},"content":[{"type":"text","text":"out"}],"isError":false,"details":{"truncation":{"content":"out","truncated":true,"truncatedBy":"lines","totalLines":9,"totalBytes":90,"outputLines":2,"outputBytes":20,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":2,"maxBytes":50000},"fullOutputPath":"C:\\Temp\\pi-powershell.log"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := goTypeName(result); got != "PowerShellToolResultEvent" {
		t.Fatalf("powershell tool_result decoded to %s, want PowerShellToolResultEvent", got)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Details struct {
			FullOutputPath string `json:"fullOutputPath"`
			Truncation     struct {
				TotalLines int `json:"totalLines"`
			} `json:"truncation"`
		} `json:"details"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Details.FullOutputPath != `C:\Temp\pi-powershell.log` || wire.Details.Truncation.TotalLines != 9 {
		t.Fatalf("powershell details did not survive decoding: %s", data)
	}
}
