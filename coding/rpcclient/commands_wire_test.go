package rpcclient

import "testing"

// RpcClient.send in upstream modes/rpc/rpc-client.ts uses serializeJsonLine
// on {...command, id}. Exercise that wire boundary without spawning a CLI.
func TestRPCCommandWirePreservesOptionalValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cmd  rpcCommand
		want string
	}{
		{"omitted images", command("prompt", append([]commandField{field("message", "<a>&\nβ")}, imagesField(nil)...)...), `{"type":"prompt","message":"<a>&\nβ","id":"req_1"}`},
		{"empty images", command("prompt", append([]commandField{field("message", "hello")}, imagesField([]ImageContent{})...)...), `{"type":"prompt","message":"hello","images":[],"id":"req_1"}`},
		{"omitted parent", command("new_session", optionalField[string]("parentSession", nil)...), `{"type":"new_session","id":"req_1"}`},
		{"empty parent", command("new_session", optionalField("parentSession", new(""))...), `{"type":"new_session","parentSession":"","id":"req_1"}`},
		{"false preserved", command("set_auto_retry", field("enabled", false)), `{"type":"set_auto_retry","enabled":false,"id":"req_1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.cmd.marshal("req_1")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want+"\n" {
				t.Fatalf("wire = %q, want %q", got, tc.want+"\n")
			}
		})
	}
}
