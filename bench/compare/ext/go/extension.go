// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package benchgo is a benchmark extension: a tool and a tool_call handler that does about 2 ms of work per call and counts its calls in
// $BENCHEXT_COUNTER (proof it ran).
package benchgo

import (
	"crypto/sha256"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("bench-go")
	ext.Tool("hello_go", "Return a hello greeting", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "hello from go"}, nil
	})
	block := make([]byte, 65536)
	ext.OnEvent("tool_call", func(ctx sdk.Context, data map[string]any) (any, error) {
		h := sha256.New()
		for range 16 {
			h.Write(block)
		}
		if path := os.Getenv("BENCHEXT_COUNTER"); path != "" {
			if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600); err == nil {
				_, _ = f.WriteString("go\n")
				_ = f.Close()
			}
		}
		return nil, nil
	})
	return ext
}
