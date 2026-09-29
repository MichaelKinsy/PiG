// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package main

import (
	"encoding/json"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// rpcJSONErrorResponse preserves UTF-16 diagnostics on the JSON boundary without changing ordinary Go error strings or the RPC response shape.
type rpcJSONErrorResponse struct {
	RPCResponse
	Error json.RawMessage `json:"error"`
}

func rpcParseError(input []byte, err error) rpcJSONErrorResponse {
	message := utf16.Encode([]rune(err.Error()))
	if !json.Valid(input) {
		if diagnostic := jsonparse.SyntaxError(input); diagnostic != nil {
			message = diagnostic
		}
	}
	message = append(utf16.Encode([]rune("Failed to parse command: ")), message...)
	return rpcJSONErrorResponse{RPCResponse: rpcError(nil, "parse", ""), Error: text.QuoteUTF16(message)}
}
