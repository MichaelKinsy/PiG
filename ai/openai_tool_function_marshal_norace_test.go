//go:build !race

package ai

import "testing"

// The completions tool list is written into the request encoder: a function tool is not encoded into its own buffer by a nested
// json.Marshal and then validated and copied into the request again.
func TestCompletionsToolsEncodeWithoutANestedMarshal(t *testing.T) {
	tools := oaiToolsForEncoding(20)
	request := oaiRequest{Model: "m", Stream: true, Tools: &tools}
	if _, err := marshalRequestBody(request); err != nil {
		t.Fatal(err)
	}
	var failed error
	allocated := allocatedBytes(func() {
		for range 10 {
			if _, err := marshalRequestBody(request); err != nil {
				failed = err
			}
		}
	})
	if failed != nil {
		t.Fatal(failed)
	}
	perTool := allocated / 10 / uint64(len(tools))
	t.Logf("%d B allocated per tool", perTool)
	if perTool > 1500 {
		t.Fatalf("encoding a completions function tool allocates %d bytes, want at most 1500 (2490 with a nested json.Marshal)", perTool)
	}
}
