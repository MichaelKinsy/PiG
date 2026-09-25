package codingagent

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Pi's createSessionId calls the shared time-ordered uuidv7 generator.
func TestGeneratedSessionIDsAreTimeOrderedUUIDv7(t *testing.T) {
	start := time.Now().UnixMilli()
	previous := ""
	for range 1024 {
		id, err := generateSessionID()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("session id %q is not a UUID: %v", id, err)
		}
		if parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
			t.Fatalf("session id %q is not RFC UUIDv7", id)
		}
		if id <= previous {
			t.Fatalf("session ids are not strictly increasing: %q <= %q", id, previous)
		}
		var ms int64
		for _, b := range parsed[:6] {
			ms = ms<<8 | int64(b)
		}
		if ms < start || ms > time.Now().UnixMilli() {
			t.Fatalf("UUIDv7 timestamp %d is outside generation interval", ms)
		}
		previous = id
	}
}

func TestSessionFilesUsePiTimestampAndKeepLegacyIDs(t *testing.T) {
	manager := NewSessionManagerWithDir(t.TempDir(), t.TempDir())
	legacy, err := manager.Create("sess-legacy123", "")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := legacy.AppendMessage(mkAssistantMsg("retained"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.Load(legacy.Path())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID() != legacy.ID() || manager.FindByID(legacy.ID()) != legacy.Path() {
		t.Fatal("existing sess- identity was changed or cannot be resolved")
	}
	for _, operation := range []string{"new", "clone", "fork"} {
		t.Run(operation, func(t *testing.T) {
			var session *Session
			var err error
			switch operation {
			case "new":
				var id string
				id, err = generateSessionID()
				if err == nil {
					session, err = manager.Create(id, "")
				}
			case "clone":
				session, err = manager.Clone(loaded, leaf)
			case "fork":
				session, err = manager.ForkFromFile(loaded.Path())
			}
			if err != nil {
				t.Fatal(err)
			}
			id, err := uuid.Parse(session.ID())
			if err != nil || id.Version() != 7 {
				t.Errorf("%s generated non-v7 id %q", operation, session.ID())
			}
			// Pi uses new Date().toISOString(), then replaces ':' and '.'.
			prefix := strings.TrimSuffix(filepath.Base(session.Path()), "_"+session.ID()+".jsonl")
			if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z$`).MatchString(prefix) {
				t.Errorf("filename timestamp %q does not use Pi's millisecond precision", prefix)
			}
			if len(session.Header().Timestamp) != len("2006-01-02T15:04:05.000Z") {
				t.Errorf("header timestamp %q does not use Pi's ISO millisecond shape", session.Header().Timestamp)
			}
		})
	}
}
