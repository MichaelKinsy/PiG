package codemode

import (
	"bytes"
	"encoding/json"
)

// decodeReply decodes the client's answer to an exchange: `null` is nil, `{ type: "stop" }` a stop, `{ type: "result", ... }`
// the result of the call the exchange carried.
func decodeReply(data []byte) (*RemoteReply, bool) {
	if string(bytes.TrimSpace(data)) == "null" {
		return nil, true
	}
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &head) != nil {
		return nil, false
	}
	switch head.Type {
	case MessageStop:
		return &RemoteReply{HostToWorkerMessage: HostToWorkerMessage{Type: MessageStop}}, true
	case MessageResult:
		var result HostToWorkerMessage
		if json.Unmarshal(data, &result) != nil {
			return nil, false
		}
		return &RemoteReply{HostToWorkerMessage: result}, true
	}
	return nil, false
}
