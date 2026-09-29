package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func authPortFile(t *testing.T, raw string) (*AuthStorage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if raw != "" {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	storage, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return storage, path
}

func authPortDisk(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err = json.Unmarshal(data, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("disk=%s want=%s", data, want)
	}
}
