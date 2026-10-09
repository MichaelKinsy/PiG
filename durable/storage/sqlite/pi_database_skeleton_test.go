//go:build portmap_skeleton

package sqlite

import "testing"

// pi-unproven: packages/durable/src/storage/sqlite/database.ts
//
// Skeleton for the marker test of packages/durable/src/storage/sqlite/database.ts. Port its cases against durable/storage/sqlite/database.go: call the entry point, assert the Pi result, then
// delete the build tag line, change `pi-unproven` to `pi`, and prove the test red by mutating a covered line of the cited file.
//
// Entry points in the cited file:
//   - type SqliteDatabase
//   - type SqliteExecutor
//   - type SqliteRow
//   - type SqliteValue
//   - type TextReader
func TestPiDurableSrcStorageSqliteDatabaseSkeleton(t *testing.T) {
	t.Fatal("skeleton: port the database.ts cases against durable/storage/sqlite/database.go")
}
