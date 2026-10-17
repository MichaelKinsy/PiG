package evals

// The PiG Session under test runs as a pig process in RPC mode. Pi's harness drives an in-process AgentSession
// (harness.ts createAgentSessionFromServices); the extension host, resource reload and tool sandbox of a PiG Session
// belong to the pig binary, so the harness drives that binary through its RPC protocol (coding/cli/rpc_mode.go).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// processStopGrace is how long a closing agent process gets to exit after its input ends before it is killed.
const processStopGrace = 10 * time.Second

type rpcLine struct {
	raw  json.RawMessage
	typ  string
	id   string
	err  error
	data json.RawMessage
	ok   bool
}

type agentProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	lines   chan rpcLine
	stderr  *tailBuffer
	nextID  int
	done    chan struct{}
	exitErr error
	// onEvent sees every line the process writes, in order, before the waiting command does.
	onEvent func(rpcLine)
}

// tailBuffer keeps the last bytes of a stream for error messages.
type tailBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (buffer *tailBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.data = append(buffer.data, p...)
	if extra := len(buffer.data) - 8192; extra > 0 {
		buffer.data = buffer.data[extra:]
	}
	return len(p), nil
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return strings.TrimSpace(string(buffer.data))
}

// startAgentProcess starts command with its pipes. The caller owns close.
func startAgentProcess(command *exec.Cmd) (*agentProcess, error) {
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	process := &agentProcess{command: command, stdin: stdin, lines: make(chan rpcLine, 64), stderr: &tailBuffer{}, done: make(chan struct{})}
	command.Stderr = process.stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	go func() {
		defer close(process.lines)
		reader := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, err := reader.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				process.lines <- parseRPCLine(line)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		process.exitErr = command.Wait()
		close(process.done)
	}()
	return process, nil
}

func parseRPCLine(line []byte) rpcLine {
	var envelope struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Success bool            `json:"success"`
		Error   string          `json:"error"`
		Data    json.RawMessage `json:"data"`
	}
	result := rpcLine{raw: json.RawMessage(strings.TrimSpace(string(line)))}
	if err := json.Unmarshal(line, &envelope); err != nil {
		result.err = err
		return result
	}
	result.typ, result.id, result.data, result.ok = envelope.Type, envelope.ID, envelope.Data, envelope.Success
	if envelope.Type == "response" && !envelope.Success {
		result.err = errors.New(envelope.Error)
	}
	return result
}

func (process *agentProcess) exited() error {
	select {
	case <-process.done:
	case <-time.After(processStopGrace):
		_ = process.command.Process.Kill()
		<-process.done
	}
	return fmt.Errorf("pig exited before answering: %w: %s", process.exitErr, process.stderr.String())
}

func (process *agentProcess) send(command map[string]any) (string, error) {
	process.nextID++
	id := strconv.Itoa(process.nextID)
	command["id"] = id
	encoded, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	_, err = process.stdin.Write(append(encoded, '\n'))
	return id, err
}

// wait reads lines until accept returns true for one. A cancelled context aborts the agent run first.
func (process *agentProcess) wait(ctx context.Context, accept func(rpcLine) bool) (rpcLine, error) {
	aborted := false
	for {
		select {
		case line, open := <-process.lines:
			if !open {
				return rpcLine{}, process.exited()
			}
			if process.onEvent != nil {
				process.onEvent(line)
			}
			if accept(line) {
				return line, nil
			}
		case <-ctx.Done():
			if aborted {
				return rpcLine{}, ctx.Err()
			}
			aborted = true
			_, _ = process.send(map[string]any{"type": "abort"})
			var cancelled context.Context
			var cancel context.CancelFunc
			cancelled, cancel = context.WithTimeout(context.WithoutCancel(ctx), processStopGrace)
			defer cancel()
			ctx = cancelled
		}
	}
}

// request sends a command and returns the data of its response.
func (process *agentProcess) request(ctx context.Context, command map[string]any) (json.RawMessage, error) {
	id, err := process.send(command)
	if err != nil {
		return nil, err
	}
	line, err := process.wait(ctx, func(line rpcLine) bool { return line.typ == "response" && line.id == id })
	if err != nil {
		return nil, err
	}
	return line.data, line.err
}

// prompt sends text and waits for the run to settle. It returns the messages of the run's agent_end event.
func (process *agentProcess) prompt(ctx context.Context, text string) ([]json.RawMessage, error) {
	id, err := process.send(map[string]any{"type": "prompt", "message": text})
	if err != nil {
		return nil, err
	}
	var messages []json.RawMessage
	var promptErr error
	_, err = process.wait(ctx, func(line rpcLine) bool {
		switch {
		case line.typ == "response" && line.id == id && line.err != nil:
			promptErr = line.err
			return true
		case line.typ == "agent_end":
			var event struct {
				Messages []json.RawMessage `json:"messages"`
			}
			_ = json.Unmarshal(line.raw, &event)
			messages = event.Messages
		case line.typ == "agent_settled":
			return true
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	return messages, promptErr
}

// close ends the process's input and waits for it to exit, killing it after processStopGrace.
func (process *agentProcess) close() error {
	_ = process.stdin.Close()
	go func() {
		for range process.lines { //nolint:revive // drain so the reader goroutine can finish
		}
	}()
	select {
	case <-process.done:
	case <-time.After(processStopGrace):
		_ = process.command.Process.Kill()
		<-process.done
	}
	return nil
}
