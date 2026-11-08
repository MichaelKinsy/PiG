package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settings.ts, locked-file.ts and observe.ts have no unit tests upstream: Pi's e2e files (m1, login, devices) drive them
// through the models plugin. These pin their own behavior.

func TestParseSettingsAcceptsCommentsAndBOMAndRejectsTheRest(t *testing.T) {
	settings, err := ParseSettings("\uFEFF{\n // a comment\n \"thinkingLevel\": \"high\", \"defaultModel\": {\"provider\": \"faux\", \"modelId\": \"faux-1\"}\n}")
	if err != nil {
		t.Fatal(err)
	}
	if settings.Value("thinkingLevel") != "high" {
		t.Fatalf("settings %v", settings)
	}
	if _, err := ParseSettings("{"); !errors.Is(err, ErrInvalidSettingsSyntax) {
		t.Fatalf("syntax: %v", err)
	}
	for _, text := range []string{`{"thinkingLevel":"loud"}`, `{"detail":5}`, `{"ownerName":"   "}`, `{"retry":{"maxRetries":-1}}`, `[]`, `{"stream":{"timeoutMs":0}}`} {
		if _, err := ParseSettings(text); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("%s: %v", text, err)
		}
	}
	if _, err := ParseSettings(`{"enabledModels":["a/b:high"],"ownerName":" Ada ","codemode":false}`); err != nil {
		t.Fatal(err)
	}
}

func TestSaveSettingsWritesIndentedJSONInKeyOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	settings, err := ParseSettings(`{"thinkingLevel":"off","defaultModel":{"provider":"faux","modelId":"faux-1"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(context.Background(), path, settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"thinkingLevel\": \"off\",\n  \"defaultModel\": {\n    \"provider\": \"faux\",\n    \"modelId\": \"faux-1\"\n  }\n}"
	if string(data) != want {
		t.Fatalf("saved:\n%s", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info, err)
	}
	if err := SaveSettings(context.Background(), path, `nope`); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("invalid save: %v", err)
	}
}

func TestLockedFileSerializesReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	file := NewLockedFile(path)
	const writers = 8
	done := make(chan error, writers)
	for range writers {
		go func() {
			_, err := WithLock(context.Background(), file, func(current *string) (LockResult[struct{}], error) {
				text := "x"
				if current != nil {
					text = *current + "x"
				}
				return LockResult[struct{}]{Next: &text}, nil
			})
			done <- err
		}()
	}
	for range writers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file starts as `{}`.
	if string(data) != "{}"+strings.Repeat("x", writers) {
		t.Fatalf("content %q", data)
	}
	file.Close()
	if _, err := WithLock(context.Background(), file, func(*string) (LockResult[int], error) { return LockResult[int]{}, nil }); !errors.Is(err, ErrLockedFileClosed) {
		t.Fatalf("after Close: %v", err)
	}
}

func TestLockedFileCancelledBeforeTheLock(t *testing.T) {
	file := NewLockedFile(filepath.Join(t.TempDir(), "x.json"))
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WithLock(ctx, file, func(*string) (LockResult[int], error) { t.Fatal("ran"); return LockResult[int]{}, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
