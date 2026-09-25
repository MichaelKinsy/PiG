package session

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

func readJsonlHeader(ctx context.Context, reader harness.TextLineReader, path string) (JsonlParsedSessionHeader, error) {
	line, err := reader.ReadLine(ctx)
	if err != nil {
		return JsonlParsedSessionHeader{}, fmt.Errorf("Failed to read JSONL storage %s: %w", path, err)
	}
	if line == nil || !line.Terminated || line.Text == "" {
		return JsonlParsedSessionHeader{}, fmt.Errorf("Invalid JSONL storage %s: missing header", path)
	}
	parsed, err := ParseJsonlSessionHeader(line.Text)
	if err != nil {
		return parsed, fmt.Errorf("Invalid JSONL storage %s: invalid header: %w", path, err)
	}
	return parsed, nil
}

// PublishFileAtomically publishes only after the synchronous writer succeeds.
func PublishFileAtomically(ctx context.Context, fs harness.FileSystem, path string, write func(appendText func(string) error) error) (err error) {
	temp := path + ".tmp"
	defer func() {
		if err != nil {
			_ = fs.Remove(ctx, temp, &harness.RemoveOptions{Force: true})
		}
	}()
	if err = fs.WriteFile(ctx, temp, nil); err != nil {
		return fmt.Errorf("Failed to stage JSONL storage %s: %w", path, err)
	}
	if err = write(func(content string) error {
		if err := fs.AppendFile(ctx, temp, []byte(content)); err != nil {
			return fmt.Errorf("Failed to append JSONL storage %s: %w", path, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if err = fs.RenameFile(ctx, temp, path); err != nil {
		return fmt.Errorf("Failed to publish JSONL storage %s: %w", path, err)
	}
	return nil
}

func publishJsonl(ctx context.Context, fs harness.FileSystem, path string, header JsonlStorageHeader, write func(appendWrites func([]CommittedWrite) error) error) error {
	return PublishFileAtomically(ctx, fs, path, func(appendText func(string) error) error {
		raw, err := json.Marshal(header)
		if err != nil {
			return err
		}
		if err := appendText(string(raw) + "\n"); err != nil {
			return err
		}
		return write(func(writes []CommittedWrite) error {
			line, err := SerializeJsonlTransaction(writes)
			if err != nil {
				return err
			}
			return appendText(line + "\n")
		})
	})
}
