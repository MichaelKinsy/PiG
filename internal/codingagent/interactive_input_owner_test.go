package codingagent

import (
	"context"
	"os"
	"testing"
)

// Theme detection and external-editor handoff must share the same stdin owner. Pi starts terminal input before theme-controller.ts:57-81, and interactive-mode.ts:954 awaits detection before extension startup.
func TestEarlyTerminalInputRetainsExternalEditorReader(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	mode := &InteractiveMode{}
	mode.startTerminalInput(ctx, input)
	if mode.inputReader == nil {
		t.Fatal("early theme input started without the external-editor reader owner")
	}
	defer func() {
		if err := mode.stopTerminalInput(); err != nil {
			t.Error(err)
		}
	}()
	reader := mode.inputReader
	readCh := mode.inputReadCh
	mode.startTerminalInput(ctx, input)
	if mode.inputReader != reader || mode.inputReadCh != readCh {
		t.Fatal("main loop replaced the existing terminal input owner")
	}
}
