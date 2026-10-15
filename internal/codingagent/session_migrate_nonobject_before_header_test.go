package codingagent

import "testing"

// pi: session-manager.ts migrateToCurrentVersion finds the header with `e.type === "session"`, which is undefined (not an error) on a number, string, boolean or array, and
// migrateV1ToV2 then writes `entry.id` on each non-header record: an array takes the property without changing, a primitive throws a strict-mode TypeError. Checked against
// Pi 1.1.0's migrateSessionEntries with the record placed before the header, where the header probe meets it.
func TestMigrateSessionEntriesMeetsARecordThatIsNotAnObjectBeforeTheHeader(t *testing.T) {
	const header = `{"type":"session","version":1,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	const message = `{"type":"message","message":{"role":"user","content":"hi","timestamp":1}}`
	for _, tc := range []struct{ name, first, wantErr string }{
		{"number", "42", "Cannot create property 'id' on number '42'"},
		{"string", `"x"`, "Cannot create property 'id' on string 'x'"},
		{"boolean", "true", "Cannot create property 'id' on boolean 'true'"},
		{"array", "[1,2]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := ParseSessionEntries(tc.first + "\n" + header + "\n" + message + "\n")
			if len(entries) != 3 {
				t.Fatalf("parsed %d records, want 3", len(entries))
			}
			err := MigrateSessionEntries(entries)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("MigrateSessionEntries = %v, want nil (Pi migrates, the array record stays as written)", err)
			case tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr):
				t.Fatalf("MigrateSessionEntries = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
