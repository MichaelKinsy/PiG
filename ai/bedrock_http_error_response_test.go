//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Pi's Bedrock client turns a non-2xx response into an exception through @smithy/core and @aws-sdk/core (the body parsed as JSON, the
// name from x-amzn-errortype, code or __type and otherwise "Unknown", the body's members copied onto the exception), and
// formatBedrockError (bedrock-converse-stream.ts:393-410) adds the status and the error-body.ts body. Each want is Pi 1.1.0's
// errorMessage for the same response (probed through AWS_BEDROCK_FORCE_HTTP1 against a local server). PiG gave the Go SDK's text:
// "UnknownError: UnknownError" for a gateway's {"error":{...}} body, and "operation error Bedrock Runtime: ConverseStream, https
// response error StatusCode: 503, …" for an HTML body, which retry.ts then matched on "503".
func TestBedrockHTTPErrorResponseIsPisSDKErrorText(t *testing.T) {
	for _, tc := range []struct {
		status    int
		errorType string
		body      string
		want      string
	}{
		{400, "", "{\"error\":{\"message\":\"nope\",\"type\":\"invalid_request_error\"}}", "Unknown: 400: {\"message\":\"nope\",\"type\":\"invalid_request_error\"}"},
		{403, "", "<html>Forbidden by gateway</html>", "Unexpected token '<', \"<html>Forb\"... is not valid JSON\n  Deserialization error: to see the raw response, inspect the hidden field {error}.$response on this object."},
		{400, "", "{\"message\":\"bad input\"}", "Unknown: bad input"},
		{400, "", "{\"code\":\"MyCode\",\"message\":\"\",\"Message\":\"cap\",\"body\":\"  raw body  \"}", "MyCode: 400: raw body"},
		{404, "", "{\"error\":{}}", "Unknown: UnknownError"},
		{400, "", "{\"Code\":42,\"statusCode\":418,\"error\":{\"b\":1,\"a\":[1.50,2e3]}}", "42: 418: {\"b\":1,\"a\":[1.5,2000]}"},
		{400, "", "[1,2]", "Unknown: UnknownError"},
		{400, "", "{\"message\":\"data retention mode 'default' is not available\"}", "Unknown: data retention mode 'default' is not available See https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html for supported data retention modes."},
		{401, "", "{\"message\":\"x\",\"error\":{\"message\":\"x\"}}", "Unknown: 401: {\"message\":\"x\"}"},
		{400, "", "{\"a\":1,}", "Expected double-quoted property name in JSON at position 7 (line 1 column 8)\n  Deserialization error: to see the raw response, inspect the hidden field {error}.$response on this object."},
		// Probed with Pi 1.1.0 the same way: the x-amzn-errortype header (cut at ':' and ','), a __type member, a capitalised
		// Message and a header-named exception with an error object; a 503 page is not JSON, so retry.ts finds no "503" in its text.
		{400, "ValidationException:http://internal.amazon.com/coral/com.amazon.bedrock/", "{\"message\":\"The model returned the following errors: input is too long\"}", "Validation error: The model returned the following errors: input is too long"},
		{400, "", "{\"__type\":\"com.amazon.coral.validate#ValidationException\",\"message\":\"bad\"}", "Validation error: bad"},
		{403, "AccessDeniedException", "{\"Message\":\"User is not authorized\"}", "AccessDeniedException: User is not authorized"},
		{400, "AccessDeniedException", "{\"message\":\"m\",\"error\":{\"x\":1}}", "AccessDeniedException: 400: {\"x\":1}"},
		{400, "Foo:bar,baz", "{\"message\":\"m2\"}", "Foo: m2"},
		{503, "", "<html>Service Unavailable</html>", "Unexpected token '<', \"<html>Serv\"... is not valid JSON\n  Deserialization error: to see the raw response, inspect the hidden field {error}.$response on this object."},
	} {
		t.Run(tc.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.errorType != "" {
					w.Header().Set("X-Amzn-Errortype", tc.errorType)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			isolateBedrockConfig(t)
			provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_BEDROCK_FORCE_HTTP1": "1"}})
			if err != nil {
				t.Fatal(err)
			}
			if message := stream.Result(); message.StopReason != StopReasonError || message.ErrorMessage != tc.want {
				t.Fatalf("stop=%s error=%q, want %q", message.StopReason, message.ErrorMessage, tc.want)
			}
		})
	}
}
