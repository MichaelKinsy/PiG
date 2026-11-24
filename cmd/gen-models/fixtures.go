package main

// Recorded upstream responses for the catalog generator. `-record <dir>` stores every response of a live run; `-replay <dir>`
// serves them back and fails any other request, as the `node --import` mock of packages/ai/test/catalog-generation-utils.ts does,
// so Pi's generator and this one can be run over the same bytes (test/parity/testdata/catalog-generation/).

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
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
	// JSON.stringify(value, null, 2) leaves <, > and & as they are; Encode ends the document with the newline the file keeps.
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	index := transport.index
	if index == nil {
		index = fixtureIndex{} // an empty recording is [], not null
	}
	if err := encoder.Encode(index); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(transport.dir, fixtureIndexFile), encoded.Bytes(), 0o644); err != nil {
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

// A catalog source is the committed recording a catalog is generated from (automation/gen/catalog-source): responses.tar.gz holds
// the responses of one live run (index.json and the bodies, in recorded order) and meta.json says when they were fetched and the
// sha256 of every body. A run that replays it is offline and deterministic, and its manifest `generatedAt` is the fetch time, so a
// regeneration from the same source writes the same bytes on any day.
const (
	sourceArchiveFile = "responses.tar.gz"
	sourceMetaFile    = "meta.json"
)

// sourceMeta is meta.json of a catalog source.
type sourceMeta struct {
	// FetchedAt is the UTC time the first response was requested, in the form of Date.prototype.toISOString.
	FetchedAt string `json:"fetchedAt"`
	// PiCommit is the Pi commit whose catalog generator (packages/ai/catalog/generate.ts) the oracle runs over this source: the upstream pin.
	PiCommit string `json:"piCommit"`
	// PiOverlayCommit is the later Pi commit the catalog files of automation/gen/catalog-source/pi-overlay come from (the pinned release's
	// generator does not have them): the provider files Pi main changed after the pin.
	PiOverlayCommit string `json:"piOverlayCommit"`
	// ArchiveSHA256 is the sha256 of responses.tar.gz.
	ArchiveSHA256 string           `json:"archiveSha256"`
	Responses     []sourceResponse `json:"responses"`
	// Merges are the -merge-source runs applied to the recording, oldest first: rows of a later live fetch taken into it (merge_source.go).
	Merges []sourceMerge `json:"merges,omitzero"`
}

type sourceResponse struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// generatedAtOverride, when set, is the `generatedAt` a generation writes instead of the clock: the fetch time of the source it replays.
var generatedAtOverride string

func generationTime() string {
	if generatedAtOverride != "" {
		return generatedAtOverride
	}
	return time.Now().UTC().Format(isoMillis)
}

// piOverlayCommit is the newest Pi commit an overlay file is taken from; each file is at the commit of the sync ledger that carries it
// (17 files at b82477b7b; families/openai.ts, providers/openai.ts, providers/openai-codex.ts and providers/qwen-token-plan-individual.ts
// at cead615b; providers/github-copilot.ts at e5c01ca6cb9d minus supportsDeveloperRole: false, without unported c815a3dfe).
const piOverlayCommit = "b82477b7bbb59e727de66ceda1fbfcdd3ccf8216"

const isoMillis = "2006-01-02T15:04:05.000Z"

// recordSource records every response of the run under a scratch directory; the returned finish packs them into dir as a catalog
// source, and is called only after the run succeeded, so a failed run leaves the committed source as it was.
func recordSource(dir string) (finish func() error, cleanup func(), err error) {
	scratch, err := os.MkdirTemp("", "catalog-source-")
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { _ = os.RemoveAll(scratch) }
	if err := recordResponses(scratch); err != nil {
		cleanup()
		return nil, nil, err
	}
	fetchedAt := time.Now().UTC().Format(isoMillis)
	return func() error { return packSource(scratch, dir, fetchedAt, nil) }, cleanup, nil
}

func packSource(recorded, dir, fetchedAt string, merges []sourceMerge) error {
	data, err := os.ReadFile(filepath.Join(recorded, fixtureIndexFile))
	if err != nil {
		return err
	}
	var index fixtureIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}
	if len(index) == 0 {
		return errors.New("no response was recorded")
	}
	meta := sourceMeta{FetchedAt: fetchedAt, PiCommit: pigversion.UpstreamCommit, PiOverlayCommit: piOverlayCommit, Merges: merges}
	var raw bytes.Buffer
	zipped := gzip.NewWriter(&raw)
	archive := tar.NewWriter(zipped)
	add := func(name string, body []byte) error {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		_, err := archive.Write(body)
		return err
	}
	if err := add(fixtureIndexFile, data); err != nil {
		return err
	}
	for _, entry := range index {
		body, err := os.ReadFile(filepath.Join(recorded, filepath.Base(entry.File)))
		if err != nil {
			return err
		}
		if err := add(filepath.Base(entry.File), body); err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		meta.Responses = append(meta.Responses, sourceResponse{URL: entry.URL, Status: entry.Status, Bytes: len(body), SHA256: hex.EncodeToString(sum[:])})
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := zipped.Close(); err != nil {
		return err
	}
	archiveSum := sha256.Sum256(raw.Bytes())
	meta.ArchiveSHA256 = hex.EncodeToString(archiveSum[:])
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(meta); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeAtomically(filepath.Join(dir, sourceArchiveFile), raw.Bytes()); err != nil {
		return err
	}
	return writeAtomically(filepath.Join(dir, sourceMetaFile), encoded.Bytes())
}

func writeAtomically(path string, data []byte) error {
	partial := path + ".partial"
	if err := os.WriteFile(partial, data, 0o644); err != nil {
		return err
	}
	return os.Rename(partial, path)
}

// replaySource makes the default client serve the committed source in dir and makes the generation time its fetch time.
func replaySource(dir string) (cleanup func(), err error) {
	meta, err := readSourceMeta(dir)
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp("", "catalog-source-")
	if err != nil {
		return nil, err
	}
	cleanup = func() { _ = os.RemoveAll(scratch) }
	if err := unpackSource(filepath.Join(dir, sourceArchiveFile), scratch, meta); err != nil {
		cleanup()
		return nil, err
	}
	if err := replayResponses(scratch); err != nil {
		cleanup()
		return nil, err
	}
	generatedAtOverride = meta.FetchedAt
	return cleanup, nil
}

func readSourceMeta(dir string) (sourceMeta, error) {
	var meta sourceMeta
	data, err := os.ReadFile(filepath.Join(dir, sourceMetaFile))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("%s: %w", filepath.Join(dir, sourceMetaFile), err)
	}
	if _, err := time.Parse(isoMillis, meta.FetchedAt); err != nil {
		return meta, fmt.Errorf("%s: fetchedAt %q: %w", filepath.Join(dir, sourceMetaFile), meta.FetchedAt, err)
	}
	return meta, nil
}

// unpackSource extracts the archive into scratch and requires every body to be the one meta.json hashes.
func unpackSource(archivePath, scratch string, meta sourceMeta) error {
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != meta.ArchiveSHA256 {
		return fmt.Errorf("%s is not the archive %s records (sha256 %x, recorded %s)", archivePath, sourceMetaFile, sum, meta.ArchiveSHA256)
	}
	zipped, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	reader := tar.NewReader(zipped)
	bodies := map[string]string{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		name := filepath.Base(header.Name)
		if err := os.WriteFile(filepath.Join(scratch, name), data, 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		bodies[name] = hex.EncodeToString(sum[:])
	}
	index, err := os.ReadFile(filepath.Join(scratch, fixtureIndexFile))
	if err != nil {
		return err
	}
	var entries fixtureIndex
	if err := json.Unmarshal(index, &entries); err != nil {
		return err
	}
	if len(entries) != len(meta.Responses) || len(bodies) != len(entries)+1 {
		return fmt.Errorf("%s holds %d responses, %s lists %d", archivePath, len(bodies)-1, sourceMetaFile, len(meta.Responses))
	}
	for i, entry := range entries {
		want := meta.Responses[i]
		if entry.URL != want.URL || entry.Status != want.Status || bodies[entry.File] != want.SHA256 {
			return fmt.Errorf("%s: response %d (%s) is not the one %s records", archivePath, i, entry.URL, sourceMetaFile)
		}
	}
	return nil
}
