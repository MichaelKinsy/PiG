package pico3

import (
	"context"
	"testing"
)

func TestPluginTaskAPIRejectsToolOnlyActions(t *testing.T) {
	t.Parallel()
	api := taskApi(Task{Id: 17, ConversationId: 29}, nil)
	if api.TaskId != 17 || api.ConversationId != 29 || api.CallId != "" {
		t.Fatalf("plugin identity = %+v", api)
	}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"stream", func() error { return api.Stream([]byte("hello")) }},
		{"progress", func() error {
			return api.Progress(context.Background(), func(*ToolProgress) { t.Error("callback invoked") })
		}},
		{"memo", func() error { _, err := api.Memo(context.Background(), "key", "value"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || err.Error() != tc.name+" is only available to tools" {
				t.Fatalf("%s error = %v", tc.name, err)
			}
		})
	}
}
