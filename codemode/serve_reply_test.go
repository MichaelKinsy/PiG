package codemode

import "testing"

// The reply of a client to an exchange as the Worker host passes it to the module: `null` (the exchange carried no call), a stop, or
// the result of the call (packages/codemode/src/runtime/remote.ts CodemodeRemoteReply).
func TestDecodeReply(t *testing.T) {
	payload := "42"
	cases := []struct {
		name  string
		in    string
		ok    bool
		check func(*RemoteReply) bool
	}{
		{"null is no reply", ` null `, true, func(r *RemoteReply) bool { return r == nil }},
		{"stop", `{"type":"stop"}`, true, func(r *RemoteReply) bool { return r != nil && r.Type == MessageStop }},
		{"result", `{"type":"result","id":7,"ok":true,"payload":"42"}`, true, func(r *RemoteReply) bool {
			return r != nil && r.Type == MessageResult && r.ID == 7 && r.Ok && r.Payload != nil && *r.Payload == payload
		}},
		{"a result for undefined has no payload", `{"type":"result","id":8,"ok":true}`, true, func(r *RemoteReply) bool { return r != nil && r.ID == 8 && r.Payload == nil }},
		{"a failed call carries its message", `{"type":"result","id":9,"ok":false,"payload":"boom"}`, true, func(r *RemoteReply) bool { return r != nil && !r.Ok && *r.Payload == "boom" }},
		{"an unknown type", `{"type":"nope"}`, false, nil},
		{"not JSON", `{`, false, nil},
		{"a bad result", `{"type":"result","id":"x"}`, false, nil},
	}
	for _, c := range cases {
		reply, ok := decodeReply([]byte(c.in))
		if ok != c.ok || (ok && !c.check(reply)) {
			t.Errorf("%s: decodeReply(%s) = %+v, %v", c.name, c.in, reply, ok)
		}
	}
}
