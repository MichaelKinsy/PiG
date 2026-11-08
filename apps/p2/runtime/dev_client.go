package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

// Ports sendDevCommand of apps/p2/src/hosts/dev-channel.ts: a command to the dev supervisor over the data directory's
// filesystem-protected socket, deliberately separate from the public HTTP server/proxy.

const (
	devCommandTimeout = 120 * time.Second
	devResultLimit    = 256 * 1024
)

// SendDevCommand sends command to the dev channel of dataDir and returns its result.
func SendDevCommand(ctx context.Context, dataDir string, command DevCommand) (DevResult, error) {
	body := map[string]any{"action": command.Action}
	if command.Plugins != nil {
		body["plugins"] = command.Plugins
	}
	//portlint:allow mapkeyorder `action` sorts before `plugins`: the keys' sorted order is Pi's insertion order
	encoded, err := json.Marshal(body)
	if err != nil {
		return DevResult{}, err
	}
	ctx, cancel := context.WithTimeoutCause(ctx, devCommandTimeout, errors.New("Dev command timed out"))
	defer cancel()
	socket := filepath.Join(dataDir, "dev.sock")
	//portlint:allow transportperrequest one command per run of a CLI, over a socket whose connection closes with it (no keep-alives): nothing to reuse
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dev/", bytes.NewReader(encoded))
	if err != nil {
		return DevResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil && ctx.Err() != nil {
			return DevResult{}, cause
		}
		return DevResult{}, err
	}
	defer response.Body.Close()
	text, err := io.ReadAll(io.LimitReader(response.Body, devResultLimit+1))
	if err != nil {
		return DevResult{}, err
	}
	if len(text) > devResultLimit {
		return DevResult{}, errors.New("Dev result too large")
	}
	var raw map[string]any
	if err := json.Unmarshal(text, &raw); err != nil {
		return DevResult{}, err
	}
	ok, okIsBool := raw["ok"].(bool)
	message, messageIsString := raw["message"].(string)
	if !okIsBool || !messageIsString {
		return DevResult{}, errors.New("Invalid dev result")
	}
	result := DevResult{OK: ok, Message: message}
	if plugins, present := raw["plugins"].([]any); present {
		names := make([]string, 0, len(plugins))
		for _, each := range plugins {
			name, _ := each.(string)
			names = append(names, name)
		}
		result.Plugins = &names
	}
	return result, nil
}
