package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports apps/p2/src/runtime/host-dirs.ts: the directories of p2's hosts, laid out as the app kit lays out its hosts
// (Chord's `node/app/layout.ts`): the server's `<data>/server/` and each session's `<data>/sessions/<id>/`, each with
// its Chord store (`store.sqlite`, mode 0600 with its journal files) and its files (`files/`, the kit's `Files`),
// directories mode 0700. Nothing here assumes two host kinds: projects become a third directory of the same shape.

// sessionID is a session id as a directory name: the catalog's UUIDs (anything else would name another directory).
var sessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ServerDirectory is the server's directory.
func ServerDirectory(dataDir string) string { return filepath.Join(dataDir, "server") }

// SessionDirectory is the directory of session id; refused for an id that is no plain name.
func SessionDirectory(dataDir, id string) (string, error) {
	if !sessionID.MatchString(id) {
		return "", fmt.Errorf("Invalid session id %s", jsonQuote(id))
	}
	return filepath.Join(dataDir, "sessions", id), nil
}

// HostStore is a host directory's store file.
func HostStore(dir string) string { return filepath.Join(dir, "store.sqlite") }

// HostFiles is a host directory's files directory (the kit's `Files`).
func HostFiles(dir string) string { return filepath.Join(dir, "files") }

// OpenHostStore opens the store of the host directory dir, made private first, with its outbox on or off (`openStore`'s
// option: stored, so passing it turns an existing store's outbox off too). p2 keeps every outbox off until it syncs.
func OpenHostStore(ctx context.Context, dir string, outbox bool) (store.Store, error) {
	if err := app.PrivateDirectory(dir); err != nil {
		return nil, err
	}
	if err := app.PrivateStore(HostStore(dir)); err != nil {
		return nil, err
	}
	database, err := node.OpenNodeSqliteDatabase(HostStore(dir), node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return nil, err
	}
	return store.OpenStore(store.SqliteBackend(database), ctx, store.StoreOptions{Outbox: &outbox})
}

// AssertMigrated refuses a data directory still in the layout before per-host stores (`server.sqlite`,
// `sessions/<id>.sqlite`): the server would start on empty stores beside them. `p2 migrate-data` moves them.
func AssertMigrated(dataDir string) error {
	old := exists(filepath.Join(dataDir, "server.sqlite"))
	if !old {
		entries, err := os.ReadDir(filepath.Join(dataDir, "sessions"))
		if err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".sqlite") {
					old = true
					break
				}
			}
		}
	}
	if old {
		return fmt.Errorf("%s holds data in p2's old layout (server.sqlite, sessions/<id>.sqlite): stop p2 and run `p2 migrate-data`", dataDir)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
