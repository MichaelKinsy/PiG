package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateToolArgs_ValidArgs(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []any{"path", "content"},
	}

	args := json.RawMessage(`{"path": "/tmp/foo.go", "content": "package main"}`)
	err := validateToolArgs("write", schema, args)
	if err != nil {
		t.Fatalf("expected valid args to pass: %v", err)
	}
}

func TestValidateToolArgs_MissingRequired(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []any{"path", "content"},
	}

	args := json.RawMessage(`{"path": "/tmp/foo.go"}`)
	err := validateToolArgs("write", schema, args)
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	// Error should mention the tool name and the args.
	if got := err.Error(); !strings.Contains(got, "write") || !strings.Contains(got, "Validation failed") {
		t.Errorf("unexpected error format: %s", got)
	}
}

func TestValidateToolArgs_WrongType(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"timeout": map[string]any{"type": "integer"},
		},
		"required": []any{"command"},
	}

	// timeout is a string instead of integer
	args := json.RawMessage(`{"command": "ls", "timeout": "not a number"}`)
	err := validateToolArgs("bash", schema, args)
	if err == nil {
		t.Fatal("expected validation error for wrong type")
	}
	if got := err.Error(); !strings.Contains(got, "bash") {
		t.Errorf("error should mention tool name: %s", got)
	}
}

func TestValidateToolArgs_EmptySchema(t *testing.T) {
	// No schema → skip validation (always passes).
	args := json.RawMessage(`{"anything": "goes"}`)
	err := validateToolArgs("custom", nil, args)
	if err != nil {
		t.Fatalf("empty schema should skip validation: %v", err)
	}
	err = validateToolArgs("custom", map[string]any{}, args)
	if err != nil {
		t.Fatalf("empty schema map should skip validation: %v", err)
	}
}

func TestValidateToolArgs_NullArgs(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}

	// Null/empty args with no required fields → valid.
	err := validateToolArgs("tool", schema, json.RawMessage("null"))
	if err != nil {
		t.Fatalf("null args with no required fields should pass: %v", err)
	}
}

func TestValidateToolArgs_InvalidJSON(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"x": map[string]any{"type": "string"},
		},
	}

	err := validateToolArgs("broken", schema, json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if got := err.Error(); !strings.Contains(got, "invalid JSON") {
		t.Errorf("should mention invalid JSON: %s", got)
	}
}

func TestValidateToolArgs_AdditionalProperties(t *testing.T) {
	// Tools commonly receive extra properties from the LLM.
	// By default JSON Schema allows additional properties.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}

	args := json.RawMessage(`{"path": "/tmp/x", "extra_field": true}`)
	err := validateToolArgs("read", schema, args)
	if err != nil {
		t.Fatalf("additional properties should be allowed by default: %v", err)
	}
}
