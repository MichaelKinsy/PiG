package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	"github.com/MichaelKinsy/PiG/ai"
)

type fixture struct {
	Name, Kind, Code, Message string
	Status                    int
	Abort                     bool
}

func frame(fields map[string]string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var headers eventstream.Headers
	for key, value := range fields {
		headers.Set(key, eventstream.StringValue(value))
	}
	var out bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&out, eventstream.Message{Headers: headers, Payload: raw}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	raw, err := os.ReadFile("test/parity/testdata/bedrock-error-cases.json")
	if err != nil {
		return err
	}
	var cases []fixture
	if err := json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	for _, test := range cases {
		if err := probe(test); err != nil {
			return err
		}
	}
	return nil
}
func probe(test fixture) error {
	start, err := frame(map[string]string{":message-type": "event", ":event-type": "messageStart", ":content-type": "application/json"}, map[string]string{"role": "assistant"})
	if err != nil {
		return err
	}
	fields := map[string]string{":message-type": test.Kind, ":content-type": "application/json"}
	if test.Kind == "exception" {
		fields[":exception-type"] = test.Code
	} else {
		fields[":error-code"] = test.Code
		fields[":error-message"] = test.Message
	}
	failure, err := frame(fields, map[string]string{"message": test.Message})
	if err != nil {
		return err
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Amzn-Requestid", "11111111-2222-3333-4444-555555555555")
		if test.Status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Amzn-Errortype", test.Code)
			w.WriteHeader(test.Status)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": test.Message})
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(append(start, failure...))
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if test.Abort {
		cancel()
	}
	provider := ai.NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}), ai.StreamOptions{CacheRetention: ai.CacheRetentionNone, Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
	if err != nil {
		return err
	}
	result := stream.Result()
	var details any
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Type == "bedrock_response_failure" {
			details = diagnostic.Details
		}
	}
	return json.NewEncoder(os.Stdout).Encode([]any{test.Name, result.StopReason, result.ErrorMessage, details})
}
