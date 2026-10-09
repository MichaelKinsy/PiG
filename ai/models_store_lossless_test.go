package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

const losslessCatalog = `{"models":[` +
	`{"id":"chat-1","extraBefore":{"a":[1,2.50,3e2]},"name":"Chat","api":"openai-completions","provider":"p","baseUrl":"https://x.test","reasoning":true,"input":["text"],"cost":{"input":1.0,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100,"headers":{},"vendorField":"kept","type":"chat"},` +
	`{"type":"image","id":"img-1","name":"Image","api":"test-image","provider":"p","baseUrl":"https://x.test","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"output":["image"],"imageExtra":null},` +
	`{"type":"classifier","zzz":1,"id":"cls-1","name":"Cls","api":"test-classifier","provider":"p","baseUrl":"https://x.test","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":10},` +
	`{"type":"future-kind","id":"later","provider":"p","anything":["goes"]}` +
	`],"lastModified":1700000000000,"checkedAt":1700000000001,"etag":"\"abc\""}`

func compactLosslessJSON(t *testing.T, data []byte) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, data); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Pi keeps a persisted catalog as plain JSON (models-store.ts ModelsStoreEntry.models: readonly AnyModel[]): reading and writing it back changes nothing.
// The typed Go models keep each record's unknown fields, field order and original value bytes (2.50, 3e2, an empty headers object), and a record of a
// model type this version does not know stays at its position.
func TestModelsStoreEntryRoundTripIsLossless(t *testing.T) {
	var entry ModelsStoreEntry
	if err := json.Unmarshal([]byte(losslessCatalog), &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry.Models) != 3 {
		t.Fatalf("typed models = %d, want the chat, image and classifier model", len(entry.Models))
	}
	if _, ok := entry.Models[0].(*Model); !ok || entry.Models[0].ModelID() != "chat-1" {
		t.Fatalf("first model = %#v", entry.Models[0])
	}
	if _, ok := entry.Models[1].(*ImageModel); !ok {
		t.Fatalf("second model = %#v", entry.Models[1])
	}
	if _, ok := entry.Models[2].(*ClassifierModel); !ok {
		t.Fatalf("third model = %#v", entry.Models[2])
	}
	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), compactLosslessJSON(t, []byte(losslessCatalog)); got != want {
		t.Fatalf("round trip changed the catalog:\n got %s\nwant %s", got, want)
	}
}

// A typed edit changes only the field it edits; unknown fields and the other fields keep their bytes and position.
func TestModelsStoreEntryEditedModelKeepsUnknownFields(t *testing.T) {
	var entry ModelsStoreEntry
	if err := json.Unmarshal([]byte(losslessCatalog), &entry); err != nil {
		t.Fatal(err)
	}
	chat := entry.Models[0].(*Model)
	chat.DisplayName = "Renamed"
	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	want := compactLosslessJSON(t, []byte(losslessCatalog))
	want = string(bytes.Replace([]byte(want), []byte(`"name":"Chat"`), []byte(`"name":"Renamed"`), 1))
	if string(out) != want {
		t.Fatalf("edited catalog:\n got %s\nwant %s", out, want)
	}
}

// The stored copy is independent (structuredClone at the store boundary) and the file store keeps the unknown fields on disk and on the way back.
func TestModelsStoreKeepsUnknownFieldsThroughCloneAndFile(t *testing.T) {
	var entry ModelsStoreEntry
	if err := json.Unmarshal([]byte(losslessCatalog), &entry); err != nil {
		t.Fatal(err)
	}
	copied := entry.Clone()
	entry.Models[0].(*Model).DisplayName = "mutated after clone"
	clonedOut, err := json.Marshal(copied)
	if err != nil || !bytes.Contains(clonedOut, []byte(`"name":"Chat"`)) || !bytes.Contains(clonedOut, []byte(`"vendorField":"kept"`)) || !bytes.Contains(clonedOut, []byte(`"future-kind"`)) {
		t.Fatalf("clone = %s (%v)", clonedOut, err)
	}

	store := NewFileModelsStore(filepath.Join(t.TempDir(), "models-store.json"))
	ctx := context.Background()
	if err := store.Write(ctx, "p", copied); err != nil {
		t.Fatal(err)
	}
	read, err := store.Read(ctx, "p")
	if err != nil || read == nil {
		t.Fatalf("read = %+v, %v", read, err)
	}
	out, err := json.Marshal(*read)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), compactLosslessJSON(t, []byte(losslessCatalog)); got != want {
		t.Fatalf("file round trip changed the catalog:\n got %s\nwant %s", got, want)
	}
}
