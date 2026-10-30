package main

// Recorded upstream responses for the catalog generator. `-record <dir>` stores every response of a live run; `-replay <dir>`
// serves them back and fails any other request, as the `node --import` mock of packages/ai/test/catalog-generation-utils.ts does,
// so Pi's generator and this one can be run over the same bytes (test/parity/testdata/catalog-generation/).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	_ "github.com/MichaelKinsy/PiG/internal/tlsdistrust" // hardens http.DefaultClient, which the generator's source fetches use (pig additive (D191))
)

// fixtureIndex is index.json of a fixture directory: the responses in the order they were recorded.
type fixtureIndex []fixtureEntry

type fixtureEntry struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	File   string `json:"file"`
}

const fixtureIndexFile = "index.json"

type recordingTransport struct {
	next  http.RoundTripper
	dir   string
	mutex sync.Mutex
	index fixtureIndex
}

func (transport *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	file := strconv.Itoa(len(transport.index)) + ".body"
	if err := os.WriteFile(filepath.Join(transport.dir, file), body, 0o644); err != nil {
		return nil, err
	}
	transport.index = append(transport.index, fixtureEntry{URL: request.URL.String(), Status: response.StatusCode, File: file})
	encoded, err := json.MarshalIndent(transport.index, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(transport.dir, fixtureIndexFile), append(encoded, '\n'), 0o644); err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

type replayTransport struct {
	dir     string
	entries map[string]fixtureEntry
}

func (transport replayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	entry, ok := transport.entries[request.URL.String()]
	if !ok {
		return nil, fmt.Errorf("Unexpected fetch: %s", request.URL)
	}
	body, err := os.ReadFile(filepath.Join(transport.dir, entry.File))
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: entry.Status, Status: strconv.Itoa(entry.Status), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
}

// recordResponses makes every request of the default client store its response under dir.
func recordResponses(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	http.DefaultClient.Transport = &recordingTransport{next: http.DefaultTransport, dir: dir}
	return nil
}

// replayResponses makes the default client serve the responses recorded under dir.
func replayResponses(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, fixtureIndexFile))
	if err != nil {
		return err
	}
	var index fixtureIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(dir, fixtureIndexFile), err)
	}
	transport := replayTransport{dir: dir, entries: map[string]fixtureEntry{}}
	for _, entry := range index {
		transport.entries[entry.URL] = entry
	}
	http.DefaultClient.Transport = transport
	return nil
}

func configureFixtures(record, replay string) error {
	switch {
	case record != "" && replay != "":
		return errors.New("-record and -replay cannot be combined")
	case record != "":
		return recordResponses(record)
	case replay != "":
		return replayResponses(replay)
	}
	return nil
}
