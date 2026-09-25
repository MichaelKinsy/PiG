package correspondence

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

const extractorOutputLimit = 16 << 20

func ExtractTypeScript(ctx context.Context, nodePath, scriptPath, sourceRoot, upstreamVersion string) (*Inventory, error) {
	if nodePath == "" || scriptPath == "" || sourceRoot == "" || upstreamVersion == "" {
		return nil, fmt.Errorf("node path, script path, source root, and upstream version are required")
	}
	command := exec.CommandContext(ctx, nodePath, scriptPath,
		"--source-root", sourceRoot,
		"--upstream-version", upstreamVersion,
		"--out", "-",
	)
	stdout := newBoundedBuffer(extractorOutputLimit)
	stderr := newBoundedBuffer(extractorOutputLimit)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if stdout.overflow || stderr.overflow {
			return nil, fmt.Errorf("correspondence extractor output exceeded %d bytes", extractorOutputLimit)
		}
		return nil, fmt.Errorf("run correspondence extractor: %w: %s", err, stderr.String())
	}
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("correspondence extractor output exceeded %d bytes", extractorOutputLimit)
	}
	inventory, err := DecodeInventory(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, err
	}
	if inventory.Source.Language != LanguageTypeScript || inventory.Source.Revision != upstreamVersion {
		return nil, fmt.Errorf("correspondence extractor returned %s %s, want TypeScript %s", inventory.Source.Language, inventory.Source.Revision, upstreamVersion)
	}
	return inventory, nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if buffer.overflow {
		return len(value), nil
	}
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.overflow = true
		return len(value), nil
	}
	write := min(len(value), remaining)
	_, _ = buffer.buffer.Write(value[:write])
	if write < len(value) {
		buffer.overflow = true
	}
	return len(value), nil
}

func (buffer *boundedBuffer) Bytes() []byte  { return buffer.buffer.Bytes() }
func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }
