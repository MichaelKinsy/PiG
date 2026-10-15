//go:build !race && !pig_strip_mistral_conversations

package ai

import "testing"

// largeWriteFollowUpBytes is what one request build after a 200 KB write call allocates, once an earlier request has quoted the arguments.
func largeWriteFollowUpBytes(t *testing.T, build func() error) uint64 {
	if err := build(); err != nil {
		t.Fatal(err)
	}
	var failed error
	allocated := allocatedBytes(func() { failed = build() })
	if failed != nil {
		t.Fatal(failed)
	}
	return allocated
}

// A finished call's arguments reach the completions body as the bytes ArgumentsJSON keeps: the request string cache finds that string by its
// identity, so a later request neither copies the 200 KB text nor hashes and compares it.
func TestCompletionsRequestSendsKeptArgumentsWithoutCopying(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: largeWriteHistory(), Frozen: true})
	provider := NewOpenAIProvider(OpenAIConfig{ProviderID: "fake", Model: "fake-model", BaseURL: "http://127.0.0.1:1/v1", APIKey: "fake"}).(*openAIProvider)
	allocated := largeWriteFollowUpBytes(t, func() error {
		conversion, err := provider.convertTranscript(transcript, nil, false)
		if err != nil {
			return err
		}
		_, err = marshalSpliced(oaiRequest{Model: "fake-model", Messages: conversion.chat, Stream: true}, requestStrings, marshalRequestBody)
		return err
	})
	t.Logf("completions follow-up request: %d B allocated", allocated)
	if allocated > 64<<10 {
		t.Fatalf("the completions request after a 200 KB write call allocates %d bytes, want at most 64 KB", allocated)
	}
}
