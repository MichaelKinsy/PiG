package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	input, err := os.ReadFile("test/parity/testdata/oauth-presence.json")
	if err != nil {
		return err
	}
	var inputs []json.RawMessage
	if err := json.Unmarshal(input, &inputs); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "oauth-presence-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx := context.Background()
	var recordings []map[string]*ai.Credential
	for i, input := range inputs {
		path := filepath.Join(dir, fmt.Sprintf("%d.json", i))
		if err := os.WriteFile(path, append(append([]byte(`{"custom":`), input...), '}'), 0600); err != nil {
			return err
		}
		readonly, err := ai.NewReadOnlyAuthStorage(path).Read(ctx, "custom")
		if err != nil {
			return err
		}
		store, err := ai.NewAuthStorage(path)
		if err != nil {
			return err
		}
		before, err := store.Read(ctx, "custom")
		if err != nil {
			return err
		}
		after, err := store.Modify(ctx, "custom", func(current *ai.Credential) (*ai.Credential, error) {
			if current.Extra == nil {
				current.Extra = map[string]json.RawMessage{}
			}
			current.Extra["note"] = json.RawMessage(`"rewritten"`)
			return current, nil
		})
		if err != nil {
			return err
		}
		fresh, err := ai.NewAuthStorage(path)
		if err != nil {
			return err
		}
		reopened, err := fresh.Read(ctx, "custom")
		if err != nil {
			return err
		}
		recordings = append(recordings, map[string]*ai.Credential{"readonly": readonly, "before": before, "after": after, "reopened": reopened})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"recordings": recordings})
}
