package rpcclient

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// rpc-client.ts parses each response line with JSON.parse, which keeps a lone UTF-16 surrogate in the text a caller reads.
func TestResponseTextKeepsALoneSurrogate(t *testing.T) {
	const line = `{"type":"response","id":"req_1","command":"get_last_assistant_text","success":true,"data":{"text":"a\ud800x\udfff<"},"error":"e\udc00"}`
	input, err := jsstring.MarshalKeepingSurrogates(line)
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := jsstring.UnmarshalKeepingSurrogates(testenv.NodeJSON(t, `const r = JSON.parse(input); return { text: r.data.text, error: r.error };`, input), &want); err != nil {
		t.Fatal(err)
	}
	client := NewRpcClient(RpcClientOptions{})
	request := pendingRequest{resolve: make(chan RpcResponse, 1), reject: make(chan error, 1)}
	client.pendingRequests["req_1"] = request
	client.handleLine([]byte(line))
	response := <-request.resolve
	data, err := getData[struct {
		Text string `json:"text"`
	}](response)
	if err != nil || data.Text != want.Text || response.Error != want.Error {
		t.Fatalf("text units=%x error units=%x err=%v, want %x and %x", jsstring.ToUTF16(data.Text), jsstring.ToUTF16(response.Error), err, jsstring.ToUTF16(want.Text), jsstring.ToUTF16(want.Error))
	}
}
