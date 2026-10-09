package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// declarations.ts:244 resolveRef: a `$ref` segment with malformed percent-encoding makes decodeURIComponent throw a URIError out of renderToolSample, so execute.ts:388 fails the codemode call instead of rendering the reference as `unknown`.
func TestExecuteFailsOnAToolSchemaWithMalformedRefEncoding(t *testing.T) {
	broken := extension.AgentTool{Name: "broken", Description: "d", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/$defs/%FF"}},"$defs":{}}`)}
	params, _ := json.Marshal(map[string]string{"code": "return 1"})
	base := extension.NewContext("", nil, func() error { return nil }, extension.ContextActions{})
	tc := extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{GetCallableTools: func() []extension.AgentTool { return []extension.AgentTool{broken} }})
	_, err := Execute(extension.WithToolContext(context.Background(), tc), "call", params, nil, Options{})
	var uriError *codemode.URIError
	if !errors.As(err, &uriError) || !strings.Contains(err.Error(), "URI malformed") {
		t.Fatalf("err=%v, want the URIError of decodeURIComponent", err)
	}
}

// The description path throws out of prepareLoadout upstream; CreateDescription returns the URIError as its error.
func TestDescriptionReturnsTheURIErrorOfAMalformedRef(t *testing.T) {
	broken := extension.AgentTool{Name: "broken", Description: "d", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/$defs/%E0%A4%A"}},"$defs":{}}`)}
	_, err := CreateDescription([]extension.AgentTool{broken}, DescriptionOptions{})
	if _, ok := errors.AsType[*codemode.URIError](err); !ok {
		t.Fatalf("CreateDescription error = %v, want the URIError", err)
	}
}
