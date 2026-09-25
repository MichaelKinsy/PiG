// Package experimental implements the opt-in local process transport used by Pi's experimental server.
package experimental

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const InternalProcessEnv = "__PI_INTERNAL_SPAWN"

// InternalProcessRole selects an internal entrypoint, not a public CLI command.
type InternalProcessRole string

// GetInternalProcessRole reads and validates the role without consuming it.
func GetInternalProcessRole() (InternalProcessRole, error) {
	role, present := os.LookupEnv(InternalProcessEnv)
	if !present {
		return "", nil
	}
	switch role {
	case "coordinator", "server", "session-worker":
		return InternalProcessRole(role), nil
	default:
		return "", fmt.Errorf("Unsupported internal process role: %s", role)
	}
}

// ConsumeInternalProcessRole validates before removing the role so descendants cannot inherit it.
func ConsumeInternalProcessRole() (InternalProcessRole, error) {
	role, err := GetInternalProcessRole()
	if err != nil {
		return "", err
	}
	if err := os.Unsetenv(InternalProcessEnv); err != nil {
		return "", err
	}
	return role, nil
}

// InternalProcessSpawnOptions supplies an optional native entry executable and environment overrides.
// EntryPath is the native counterpart of a source module URL; an empty path re-executes this executable.
type InternalProcessSpawnOptions struct {
	EntryPath string
	Env       map[string]string
}

// InternalProcess owns the child's exit observation and reaps it exactly once.
// A detached child may outlive its launcher. Done closes only after Wait has reaped it.
type InternalProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// PID returns the spawned process ID.
func (p *InternalProcess) PID() int { return p.cmd.Process.Pid }

// Done closes when the child can no longer take ownership of a socket or session.
func (p *InternalProcess) Done() <-chan struct{} { return p.done }

// Wait joins exit observation and returns the child's exit error, if any.
func (p *InternalProcess) Wait() error {
	<-p.done
	return p.err
}

// ProcessState returns nil while the process runs and its final state after it exits.
func (p *InternalProcess) ProcessState() *os.ProcessState {
	select {
	case <-p.done:
		return p.cmd.ProcessState
	default:
		return nil
	}
}

// SpawnInternalProcess starts a detached native role with ignored stdio and the current working directory.
// The caller must select an executable that explicitly dispatches internal roles. Stock CLI dispatch is unchanged.
func SpawnInternalProcess(role InternalProcessRole, args []string, options InternalProcessSpawnOptions) (*InternalProcess, error) {
	entry := options.EntryPath
	if entry == "" {
		var err error
		entry, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(entry, args...)
	cmd.Env = os.Environ()
	for key, value := range options.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	// exec.Cmd keeps the last value for each environment key.
	cmd.Env = append(cmd.Env, InternalProcessEnv+"="+string(role))
	cmd.SysProcAttr = internalProcessAttributes()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	child := &InternalProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		child.err = cmd.Wait()
		close(child.done)
	}()
	return child, nil
}

// TerminateInternalProcess forces a child to exit and waits until it can no longer take ownership.
// A nonzero exit status is expected after a kill and is available separately through Wait.
func TerminateInternalProcess(child *InternalProcess) error {
	if child == nil {
		return nil
	}
	select {
	case <-child.done:
		return nil
	default:
	}
	if err := child.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	<-child.done
	return nil
}

const MaxControlLineBytes = 128 * 1024 * 1024

// EncodeControlLine encodes one value with JSON.stringify number, string and object-key semantics and a newline, enforcing Pi's UTF-8 byte limit including the delimiter.
func EncodeControlLine(message any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return "", err
	}
	encoded, err := canonicalControlJSON(buffer.Bytes())
	if err != nil {
		return "", err
	}
	if len(encoded) >= MaxControlLineBytes {
		return "", errors.New("Internal control message is too large")
	}
	return string(encoded) + "\n", nil
}
