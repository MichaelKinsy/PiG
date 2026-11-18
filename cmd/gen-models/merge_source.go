package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/modelgen"
)

// -merge-source takes named models of the models.dev response from one live fetch into the committed catalog source
// (automation/gen/catalog-source) and leaves every other byte of every response as the committed recording has it. It exists for a
// release line that follows Pi to a commit and must hold exactly the rows that commit's catalog code is responsible for, not the
// whole of today's upstream data (that is a full `make model-catalogs-live`). Every merged row is real upstream data of one dated
// fetch: meta.json keeps the committed recording's fetchedAt and records, per merge, the live fetch's time, the response and the ids.
//
// A model is named `<provider>/<id>`, a provider and a model of the models.dev v2 response (modelgen.ModelsDevURL). The live model is
// copied whole, with its own bytes, into the committed provider's `models` object right after the nearest preceding model of the live
// listing the committed one holds (first when none), so the generator sees the row where models.dev lists it. A model the live
// response does not hold, a model the committed recording already holds and a provider the recording lacks are errors, and the source
// is left as it was.

// sourceMerge is one -merge-source run as meta.json records it.
type sourceMerge struct {
	// Response is the URL of the response the rows were merged into.
	Response string `json:"response"`
	// FetchedAt is the UTC time of the live fetch the rows come from.
	FetchedAt string `json:"fetchedAt"`
	// IDs are the merged models, `<provider>/<id>`.
	IDs []string `json:"ids"`
}

// member is one member of a JSON object: the quoted key spans [keyStart, keyEnd) and the member, key and value, [keyStart, end).
type member struct {
	key              string
	keyStart, keyEnd int
	valueStart, end  int
}

// objectSpan is the object at a path of a JSON document: the offsets of its braces and its members in order.
type objectSpan struct {
	open, close int
	members     []member
}

func (span objectSpan) find(key string) (member, bool) {
	for _, m := range span.members {
		if m.key == key {
			return m, true
		}
	}
	return member{}, false
}

// objectAt locates the object reached from the document's top-level object through path.
func objectAt(body []byte, path ...string) (objectSpan, error) {
	span, err := scanObject(json.NewDecoder(bytes.NewReader(body)), body, path)
	if err != nil {
		return objectSpan{}, fmt.Errorf("%s: %w", strings.Join(path, "."), err)
	}
	return span, nil
}

func scanObject(dec *json.Decoder, body []byte, path []string) (objectSpan, error) {
	token, err := dec.Token()
	if err != nil {
		return objectSpan{}, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return objectSpan{}, errors.New("not an object")
	}
	span := objectSpan{open: int(dec.InputOffset()) - 1}
	found := len(path) == 0
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return objectSpan{}, err
		}
		key, _ := keyToken.(string)
		keyEnd := int(dec.InputOffset())
		if len(path) > 0 && key == path[0] && !found {
			sub, err := scanObject(dec, body, path[1:])
			if err != nil {
				return objectSpan{}, err
			}
			span, found = sub, true
			continue
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return objectSpan{}, err
		}
		if len(path) == 0 {
			end := int(dec.InputOffset())
			span.members = append(span.members, member{key: key, keyStart: quoteStart(body, keyEnd), keyEnd: keyEnd, valueStart: end - len(raw), end: end})
		}
	}
	if _, err := dec.Token(); err != nil {
		return objectSpan{}, err
	}
	if len(path) == 0 {
		span.close = int(dec.InputOffset()) - 1
		return span, nil
	}
	if !found {
		return objectSpan{}, fmt.Errorf("no member %q", path[0])
	}
	return span, nil
}

// quoteStart is the offset of the quote that opens the string ending just before end.
func quoteStart(body []byte, end int) int {
	for i := end - 2; i >= 0; i-- {
		if body[i] != '"' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && body[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return i
		}
	}
	return 0
}

// mergeModels returns committed with the named models of live inserted, or an error naming the first model that cannot be taken.
func mergeModels(committed, live []byte, ids []string) ([]byte, error) {
	body := committed
	for _, id := range ids {
		provider, model, ok := strings.Cut(id, "/")
		if !ok || provider == "" || model == "" {
			return nil, fmt.Errorf("%q is not <provider>/<model>", id)
		}
		liveSpan, err := objectAt(live, provider, "models")
		if err != nil {
			return nil, fmt.Errorf("live response: %w", err)
		}
		taken, ok := liveSpan.find(model)
		if !ok {
			return nil, fmt.Errorf("the live response holds no model %s", id)
		}
		current, err := objectAt(body, provider, "models")
		if err != nil {
			return nil, fmt.Errorf("committed recording: %w", err)
		}
		if _, exists := current.find(model); exists {
			return nil, fmt.Errorf("the committed recording already holds %s", id)
		}
		pos, inserted := current.open+1, live[taken.keyStart:taken.end]
		var predecessor *member
		for _, m := range liveSpan.members {
			if m.key == model {
				break
			}
			if held, ok := current.find(m.key); ok {
				predecessor = &held
			}
		}
		switch {
		case predecessor != nil:
			pos, inserted = predecessor.end, append([]byte(","), inserted...)
		case len(current.members) > 0:
			inserted = append(append([]byte(nil), inserted...), ',')
		}
		merged := make([]byte, 0, len(body)+len(inserted))
		merged = append(append(append(merged, body[:pos]...), inserted...), body[pos:]...)
		body = merged
	}
	return body, nil
}

// mergeSource merges the named models of fetch(modelgen.ModelsDevURL) into the catalog source in dir.
func mergeSource(dir string, ids []string, fetch func(url string) ([]byte, error), now func() time.Time) error {
	if len(ids) == 0 {
		return errors.New("-merge-ids names no model")
	}
	meta, err := readSourceMeta(dir)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "catalog-source-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	if err := unpackSource(filepath.Join(dir, sourceArchiveFile), scratch, meta); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(scratch, fixtureIndexFile))
	if err != nil {
		return err
	}
	var index fixtureIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}
	target := -1
	for i, entry := range index {
		if entry.URL == modelgen.ModelsDevURL {
			target = i
		}
	}
	if target < 0 {
		return fmt.Errorf("the catalog source holds no response of %s", modelgen.ModelsDevURL)
	}
	file := filepath.Join(scratch, filepath.Base(index[target].File))
	committed, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	live, err := fetch(modelgen.ModelsDevURL)
	if err != nil {
		return err
	}
	fetchedAt := now().UTC().Format(isoMillis)
	merged, err := mergeModels(committed, live, ids)
	if err != nil {
		return err
	}
	if err := os.WriteFile(file, merged, 0o644); err != nil {
		return err
	}
	merges := append(append([]sourceMerge(nil), meta.Merges...), sourceMerge{Response: modelgen.ModelsDevURL, FetchedAt: fetchedAt, IDs: ids})
	return packSource(scratch, dir, meta.FetchedAt, merges)
}

// fetchLive reads one URL with the default client, which the tests replace.
func fetchLive(url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %d", url, response.StatusCode)
	}
	return io.ReadAll(response.Body)
}

func runMergeSource(dir, idList string) int {
	var ids []string
	for id := range strings.SplitSeq(idList, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if err := mergeSource(dir, ids, fetchLive, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Println("Merged", strings.Join(ids, ", "), "from the live models.dev response into the catalog source under", dir)
	return 0
}
