package store

import (
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/catalog.ts
//
// The catalog (slice 6a §2, §5): the table definitions the store records (version, state, columns, key, fingerprint,
// indexes), read once at open, changed only by `table`, `index`, `dropIndex` and `alter` writes inside a step, and in
// memory again only after `COMMIT` returned.
//
// Go forced difference: a TableRecord's maps are ordered slices (Columns, Indexes) so the stored JSON has Pi's key
// order; the stored bytes are `JSON.stringify(record)`.

// TableState is a table's state: `migrating` while an eager migration of `version` runs (slice 6a §9).
type TableState string

// The table states.
const (
	StateReady     TableState = "ready"
	StateMigrating TableState = "migrating"
)

// IndexRecord is an index definition: its name and declared columns (the key is appended when rendered).
type IndexRecord struct {
	Name    string
	Columns []string
}

// IndexRecords are index definitions in order, serialized as a JSON object `{ [name]: columns }`.
type IndexRecords []IndexRecord

// Get is the declared columns of index name.
func (records IndexRecords) Get(name string) ([]string, bool) {
	for _, record := range records {
		if record.Name == name {
			return record.Columns, true
		}
	}
	return nil, false
}

// MarshalJSON is the JSON object of the indexes, in order.
func (records IndexRecords) MarshalJSON() ([]byte, error) {
	out := []byte{'{'}
	for index, record := range records {
		if index > 0 {
			out = append(out, ',')
		}
		out = jsstring.AppendString(out, record.Name)
		out = append(out, ':')
		columns := record.Columns
		if columns == nil {
			columns = []string{}
		}
		text, err := jsstring.MarshalJSON(columns)
		if err != nil {
			return nil, err
		}
		out = append(out, text...)
	}
	return append(out, '}'), nil
}

// IndexRecordsFromJSON reads a decoded JSON object (an *delta.JsonObject) of name to column-name arrays.
func IndexRecordsFromJSON(value any) (IndexRecords, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, errors.New("store: index records: expected an object")
	}
	records := make(IndexRecords, 0, object.Len())
	for name, raw := range object.All() {
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("store: index %q: expected an array", name)
		}
		columns := make([]string, len(items))
		for index, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("store: index %q: expected column names", name)
			}
			columns[index] = text
		}
		records = append(records, IndexRecord{Name: name, Columns: columns})
	}
	return records, nil
}

// TableRecord is a table as the store records it, by its SQL name. Fields serialize in Pi's key order.
type TableRecord struct {
	Table   string        `json:"table"`
	Version int           `json:"version"`
	State   TableState    `json:"state"`
	Columns ColumnRecords `json:"columns"`
	Key     []string      `json:"key"`
	// Fingerprint is the table's schema fingerprint.
	Fingerprint string `json:"fingerprint"`
	// Indexes are the index definitions by name: their declared columns (the key is appended when rendered).
	Indexes IndexRecords `json:"indexes"`
}

// TableRecordFromJSON reads a decoded JSON table record (as stored).
func TableRecordFromJSON(value any) (*TableRecord, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, errors.New("store: table record: expected an object")
	}
	record := &TableRecord{}
	record.Table, _ = object.Value("table").(string)
	version, _ := object.Value("version").(float64)
	record.Version = int(version)
	state, _ := object.Value("state").(string)
	record.State = TableState(state)
	record.Fingerprint, _ = object.Value("fingerprint").(string)
	var err error
	if record.Columns, err = ColumnRecordsFromJSON(object.Value("columns")); err != nil {
		return nil, err
	}
	keys, _ := object.Value("key").([]any)
	record.Key = make([]string, len(keys))
	for index, key := range keys {
		record.Key[index], _ = key.(string)
	}
	if record.Indexes, err = IndexRecordsFromJSON(object.Value("indexes")); err != nil {
		return nil, err
	}
	return record, nil
}

// ReadCatalog is every recorded table, by SQL name.
func ReadCatalog(executor sqlite.SqliteExecutor) (map[string]*TableRecord, error) {
	rows, err := executor.All(`SELECT name, definition FROM ` + TableTables)
	if err != nil {
		return nil, err
	}
	catalog := make(map[string]*TableRecord, len(rows))
	for _, row := range rows {
		name, _ := row["name"].(string)
		definition, _ := row["definition"].(string)
		parsed, err := delta.DecodeJson([]byte(definition))
		if err != nil {
			return nil, err
		}
		record, err := TableRecordFromJSON(parsed)
		if err != nil {
			return nil, err
		}
		catalog[name] = record
	}
	return catalog, nil
}

// WriteCatalog writes the table records step changed, once each, inside its backend transaction. Go difference: a Go
// map has no insertion order, so the records are written in name order (the stored state is the same).
func WriteCatalog(step *Step) error {
	names := make([]string, 0, len(step.Tables))
	for name := range step.Tables {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		definition, err := jsstring.MarshalJSON(step.Tables[name])
		if err != nil {
			return err
		}
		if err := step.Run(`INSERT OR REPLACE INTO `+TableTables+` (name, definition) VALUES (?, ?)`, name, string(definition)); err != nil {
			return err
		}
	}
	return nil
}
