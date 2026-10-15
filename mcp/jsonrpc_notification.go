package mcp

import "math"

// IsJSONRPCRequest is upstream isJsonRpcRequest (protocol/jsonrpc.ts): a JSON-RPC 2.0 object with a string or finite-number id and a string method. message is a parsed [JSONRPCMessage] or a decoded JSON object (map[string]any); any other value is no request.
func IsJSONRPCRequest(message any) bool {
	switch value := message.(type) {
	case JSONRPCMessage:
		return value.IsRequest()
	case map[string]any:
		_, hasMethod := value["method"].(string)
		return value["jsonrpc"] == "2.0" && isJSONRPCID(value["id"]) && hasMethod
	}
	return false
}

// IsJSONRPCNotification is upstream isJsonRpcNotification (protocol/jsonrpc.ts): a JSON-RPC 2.0 object with a string method and no id member (an id of null still counts as an id member). message takes the same values as [IsJSONRPCRequest].
func IsJSONRPCNotification(message any) bool {
	switch value := message.(type) {
	case JSONRPCMessage:
		return value.IsNotification()
	case map[string]any:
		_, hasID := value["id"]
		_, hasMethod := value["method"].(string)
		return value["jsonrpc"] == "2.0" && !hasID && hasMethod
	}
	return false
}

// IsJSONRPCResponse is upstream isJsonRpcResponse (protocol/jsonrpc.ts): a JSON-RPC 2.0 object with a valid id that carries a result and no error, or an error object with a numeric code and a string message. message takes the same values as [IsJSONRPCRequest].
func IsJSONRPCResponse(message any) bool {
	switch value := message.(type) {
	case JSONRPCMessage:
		return value.IsResponse()
	case map[string]any:
		if value["jsonrpc"] != "2.0" || !isJSONRPCID(value["id"]) {
			return false
		}
		if _, hasResult := value["result"]; hasResult {
			_, hasError := value["error"]
			return !hasError
		}
		failure, ok := value["error"].(map[string]any)
		if !ok {
			return false
		}
		_, codeIsNumber := failure["code"].(float64)
		_, messageIsString := failure["message"].(string)
		return codeIsNumber && messageIsString
	}
	return false
}

// isJSONRPCID is upstream isJsonRpcId: a string or a finite number.
func isJSONRPCID(value any) bool {
	switch id := value.(type) {
	case string:
		return true
	case float64:
		return !math.IsNaN(id) && !math.IsInf(id, 0)
	}
	return false
}
