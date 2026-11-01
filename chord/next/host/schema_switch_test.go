package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/schema/{declarations,owner,proofs,write-stored}.ts and schema-switch.test.ts: the tests of
// the schema switch from TypeBox to pi-schema (decision "A schema package beneath pi-ai and Chord"). The same
// declarations give byte-identical fingerprints and normal forms, and data stored by the code before the switch loads
// after it. `testdata/schema-switch/recorded.json` and `stored/` are Pi's files, byte for byte: they were made with the
// TypeBox helpers (`write-stored.ts`, which Pi keeps "as it was written; rerun only to make a new fixture on purpose",
// so the Go test does not rerun it: the checksums below pin the copies to Pi's files).
//
// Go forms: Pi's `Static`-typed declarations are the schema data of chord/next/contracts; `expect(...).toEqual` is a
// structural comparison of the JSON values (ssStructurallyEqual), and every fingerprint and normal form is
// additionally compared as the exact text Pi recorded (ssSameText), so a fingerprint or a key order that differs by one
// byte fails.

const (
	// ssStoreSHA256, ssArchiveSHA256 and ssRecordedSHA256 are the checksums of Pi's `stored/store.db`,
	// `stored/archive.jsonl` and `recorded.json` at b556cfe5.
	ssStoreSHA256    = "b425154bd71cf9aa0110400232a1f69b3341ae462186d9c2f7ba8c9ab0c91cdc"
	ssArchiveSHA256  = "b6d790dcedc2fe6af536e7f0fba52a966eb47d9756fcce3eccc529fbda175657"
	ssRecordedSHA256 = "79a8b1266d4ff085c333d835805977e34ab3d99b0aeff10bb7f4a1dbe53d5dc6"
)

// --- declarations.ts -----------------------------------------------------------------------------------------------

type ssDeclarations struct {
	everything contracts.Schema
	Everything *contracts.Contract
	Account    *contracts.Contract
	Profile    *documents.StateDefinition
	Notes      *documents.StateDefinition
	Rows       *store.TableDefinition
	EditsV1    *store.TableDefinition
	EditsV2    *store.TableDefinition
	EditsV3    *store.TableDefinition
	ImportJob  *JobDefinition
	Owner      *contracts.Contract

	importState *documents.StateDefinition
}

var (
	ssDeclared = sync.OnceValue(declareSchemaSwitch)
)

// ssMust returns value, panicking on err (declarations are package state, like Pi's module-level ones).
func ssMust[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func declareSchemaSwitch() *ssDeclarations {
	c := contracts.Prop
	str, num, intS, boolS := contracts.Str, contracts.Num, contracts.Int, contracts.Bool
	d := &ssDeclarations{}
	// A schema with every helper and option a declaration can use.
	d.everything = contracts.Obj(
		c("name", str(contracts.Min(1), contracts.Max(40), contracts.Pattern("^[a-z ]+$"))),
		c("nick", str(contracts.Default("x"))),
		c("age", intS(contracts.Min(0), contracts.Max(150))),
		c("count", intS(contracts.Default(0))),
		c("score", num(contracts.Min(0), contracts.Max(100))),
		c("ratio", num(contracts.Default(0.5))),
		c("active", boolS()),
		c("verified", boolS(contracts.Default(false))),
		c("role", contracts.OneOf("admin", "user", "guest")),
		c("level", contracts.OneOf(1, 2, 3)),
		c("kind", contracts.Lit("person")),
		c("one", contracts.Lit(1)),
		c("yes", contracts.Lit(true)),
		c("tags", contracts.Arr(str(), contracts.Min(0), contracts.Max(5))),
		c("points", contracts.Arr(intS())),
		c("extra", contracts.Map(intS())),
		c("note", contracts.Opt(str())),
		c("nothing", contracts.Nul()),
		c("choice", contracts.Union([]contracts.Schema{str(), intS(), contracts.Obj(c("deep", contracts.Arr(contracts.Obj(c("x", num())))))})),
		c("nested", contracts.Obj(c("inner", contracts.Opt(contracts.Obj(c("v", str())))))),
	)
	// A local contract with state, keyed state and methods of every argument shape.
	d.Everything = exLocal("fixture.switch.everything", contracts.Params{c("id", str()), c("shard", intS())},
		contracts.Mem("profile", d.everything),
		contracts.Mem("notes", contracts.Keyed(contracts.Obj(c("body", str())))),
		contracts.Mem("plain", contracts.NewMethod()),
		contracts.Mem("args", contracts.NewMethod(contracts.Obj(c("a", str()), c("b", contracts.Opt(intS())), c("any", contracts.Unknown())))),
		contracts.Mem("result", contracts.NewMethod(contracts.Schema{}, contracts.Arr(str()))),
		contracts.Mem("both", contracts.NewMethod(contracts.Obj(c("when", intS(contracts.Min(0)))),
			contracts.Union([]contracts.Schema{contracts.Lit("ok"), contracts.Obj(c("error", str()))}))),
		contracts.Mem("blob", contracts.NewMethod(contracts.Obj(c("data", contracts.Bytes(contracts.Max(1024)))), contracts.Bytes())),
	)
	serverKind := ssMust(contracts.NewHostKind("fixture.switch.server", c("region", str())))
	// A remote contract with variants and a host address.
	d.Account = exRemote("fixture.switch.account", contracts.Params{c("user", str(contracts.Min(1)))},
		func(*delta.JsonObject) (contracts.Address, error) {
			return serverKind.Address(jsonObject("region", "eu")), nil
		},
		contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{c("balance", num())},
			Methods: []contracts.MethodEntry{contracts.Meth("pay", contracts.NewMethod(contracts.Obj(c("amount", num(contracts.Min(0)))), boolS()))},
		}),
		contracts.Var("guest", contracts.VariantDeclaration{State: []contracts.Property{c("balance", num())}}),
	)
	// A persisted singleton document of `everything`.
	d.Profile = exDefineState("fixture.switch.profile", documents.StateOptions{
		Schema: d.everything,
		Initial: func() *delta.JsonObject {
			return jsonObject(
				"name", "ada", "nick", "x", "age", 36.0, "count", 0.0, "score", 1.0, "ratio", 0.5, "active", true, "verified", false,
				"role", "admin", "level", 1.0, "kind", "person", "one", 1.0, "yes", true, "tags", []any{}, "points", []any{},
				"extra", jsonObject(), "nothing", nil, "choice", "a", "nested", jsonObject())
		},
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 2
		},
	})
	// A persisted keyed document.
	d.Notes = exDefineState("fixture.switch.notes", documents.StateOptions{
		Schema:         contracts.Obj(c("body", str()), c("tags", contracts.Opt(contracts.Arr(str())))),
		Initial:        func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:          true,
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	// A table with defaults, an optional column and a `json(...)` column.
	d.Rows = ssMust(store.DefineTable("fixture.switch.rows", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()),
			store.Col("n", store.Int(contracts.Default(0))),
			store.Col("flag", store.Bool(contracts.Default(false))),
			store.Col("label", store.Opt(store.Str())),
			store.Col("data", store.Json(contracts.Obj(c("a", intS()), c("b", contracts.Opt(contracts.Arr(str())))), store.JsonColumnOptions{Version: 1})),
			store.Col("maybe", store.Opt(store.Json(contracts.Map(num()), store.JsonColumnOptions{Version: 1}))),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_n", Columns: []string{"n", "id"}}},
	}))
	// A table evolved over three versions: a rename, an added column with a default, a json column, a drop.
	d.EditsV1 = ssMust(store.DefineTable("fixture.switch.edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("file", store.Str()), store.Col("at", store.Int())},
		Key:     []string{"id"},
	}))
	d.EditsV2 = ssMust(store.EvolveTable(d.EditsV1, func(e *store.TableEditor) *store.TableEditor {
		return e.Rename("file", "path").Add("dir", store.Str(contracts.Default(""))).Rows(func(row store.Row) (any, error) {
			path := row["path"].(string)
			return store.Row{"dir": path[:strings.LastIndex(path, "/")]}, nil
		})
	}))
	d.EditsV3 = ssMust(store.EvolveTable(d.EditsV2, func(e *store.TableEditor) *store.TableEditor {
		return e.Drop("dir").Index("by_path", []string{"path", "at"})
	}))
	// A persisted job with a state document.
	importState := exDefineState("fixture.switch.import.state", documents.StateOptions{
		Schema:         contracts.Obj(c("done", intS())),
		Initial:        func() *delta.JsonObject { return jsonObject("done", 0.0) },
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	d.ImportJob = ssMust(DefineJob("fixture.switch.import", JobOptions{
		Version: 1,
		Args:    contracts.Obj(c("path", str())),
		Result:  contracts.Obj(c("count", intS())),
		State:   importState,
		Persist: &JobPersistence{Resume: ResumeRerun},
	}))
	d.importState = importState
	// The object owning the persisted documents and the tables (a local type with empty params, as tables need).
	d.Owner = exLocal("fixture.switch.owner", contracts.Params{},
		contracts.Mem("profile", d.everything),
		contracts.Mem("write", contracts.NewMethod(contracts.Obj(c("id", str()), c("body", str())))),
		contracts.Mem("read", contracts.NewMethod(str(), str())),
		contracts.Mem("dump", contracts.NewMethod(contracts.Obj(), contracts.Unknown())),
	)
	return d
}

// --- owner.ts ------------------------------------------------------------------------------------------------------

// ssBehaviour is what the job's `run` does in this process: wait until its context ends, or finish at once.
type ssBehaviour string

const (
	ssWait   ssBehaviour = "wait"
	ssFinish ssBehaviour = "finish"
)

// ssFinished collects the finished runs' results, in order (Pi's module-level `finished`).
type ssFinished struct {
	mu    sync.Mutex
	items []any
}

func (f *ssFinished) add(item any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, item)
}

func (f *ssFinished) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.items)
}

func (f *ssFinished) all() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any{}, f.items...)
}

// ssOwnerFacet is `ownerFacet(edits, behaviour)`: the owner's facet over `edits` (the table version this process
// declares). host is the host the facet is served by: `imports.running.value` is read inside one of its sections.
func ssOwnerFacet(t testing.TB, d *ssDeclarations, edits *store.TableDefinition, behaviour ssBehaviour, finished *ssFinished, host func() Host) func(FacetHost, FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		return h.Serve(d.Owner, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			profile := stateOf(self, d.Profile)
			notes := self.State(d.Notes).(*documents.Keyed)
			rows := self.(StoreSelf).Table(d.Rows)
			editsTable := self.(StoreSelf).Table(edits)
			imports := self.Job(d.ImportJob, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					path := exParam(args.(*delta.JsonObject), "path")
					if _, err := ctx.State().Change(ctx, func(draft *delta.Object) (any, error) {
						return nil, draft.Set("done", draft.Get("done").(float64)+float64(len(path)))
					}, ChangeOptions{}); err != nil {
						return nil, err
					}
					if behaviour == ssWait {
						UntilAborted(ctx)
						return nil, context.Cause(ctx)
					}
					return jsonObject("count", mustState(ctx).Value("done")), nil
				},
				Finish: func(outcome Outcome, _ documents.Tx) error {
					if outcome.Kind == OutcomeDone {
						finished.add(outcome.Result)
					} else {
						finished.add(string(outcome.Kind))
					}
					return nil
				},
			})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"profile": profile.Member()}, map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					id, body := exParam(args, "id"), exParam(args, "body")
					if _, err := self.Tx(func(tx documents.Tx) (any, error) {
						note, err := tx.Draft(notes, id)
						if err != nil {
							return nil, err
						}
						if err := note.Set("body", body); err != nil {
							return nil, err
						}
						draft, err := tx.Draft(profile.s)
						if err != nil {
							return nil, err
						}
						count := draft.Get("count").(float64) + 1
						if err := draft.Set("count", count); err != nil {
							return nil, err
						}
						if _, err := draft.Array("tags").Push(id); err != nil {
							return nil, err
						}
						if err := draft.Object("extra").Set(id, float64(len(body))); err != nil {
							return nil, err
						}
						if err := draft.Set("choice", jsonObject("deep", []any{jsonObject("x", count)})); err != nil {
							return nil, err
						}
						if err := draft.Set("nested", jsonObject("inner", jsonObject("v", body))); err != nil {
							return nil, err
						}
						row := store.Row{"id": id, "label": body, "data": jsonObject("a", float64(len(body)), "b", []any{id})}
						if id == "a" {
							row["maybe"] = jsonObject("n", 1.5)
						}
						if err := tx.Put(rows, row); err != nil {
							return nil, err
						}
						return nil, tx.Put(editsTable, store.Row{"id": id, "file": "/notes/" + id + ".md", "at": int64(len(body))})
					}, ctx); err != nil {
						return nil, err
					}
					_, err := imports.Start(jsonObject("path", id), ctx, documents.StartOptions{})
					return nil, err
				}),
				"read": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					note, err := notes.Get(ctx, id.(string))
					if err != nil || note == nil {
						return "", err
					}
					return note.Value("body"), nil
				},
				"dump": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					noteOrNull := func(id string) (any, error) {
						note, err := notes.Get(ctx, id)
						if err != nil || note == nil {
							return nil, err
						}
						return note, nil
					}
					a, err := noteOrNull("a")
					if err != nil {
						return nil, err
					}
					b, err := noteOrNull("b")
					if err != nil {
						return nil, err
					}
					rowItems, err := ssPage(rows, d.Rows, ctx)
					if err != nil {
						return nil, err
					}
					editItems, err := ssPage(editsTable, edits, ctx)
					if err != nil {
						return nil, err
					}
					runs := 0
					host().(*chordHost).enter(func() {
						running, err := imports.Running().Value()
						must(t, err)
						runs = len(running.([]any))
					})
					return jsonObject("profile", profile.Value(), "notes", jsonObject("a", a, "b", b),
						"rows", rowItems, "edits", editItems, "runs", float64(runs)), nil
				}),
			}), nil
		}, ServeOptions{})
	}
}

// ssPage is `(await table.page(query(definition), 10, ctx)).items` as JSON: each row's keys in column order, an absent
// optional column left out as JSON.stringify leaves out `undefined`.
func ssPage(table *store.TableHandle, definition *store.TableDefinition, ctx context.Context) ([]any, error) {
	page, err := table.Page(ctx, store.NewQuery(definition), 10, store.PageOptions{})
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, row := range page.Items {
		item := delta.NewJsonObject(len(definition.Columns))
		for _, column := range definition.Columns {
			if value, ok := row[column.Name]; ok && value != nil {
				item.Set(column.Name, ssJSON(value))
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// ssJSON is a store value as JSON: integers are numbers.
func ssJSON(value any) any {
	switch typed := value.(type) {
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	}
	return value
}

// ssOwnerHost is `ownerHost(path, edits, behaviour, context)`: a host serving the owner on the SQLite store at path.
func ssOwnerHost(t *testing.T, d *ssDeclarations, path string, edits *store.TableDefinition, behaviour ssBehaviour, finished *ssFinished) (*store.ChordStore, Host) {
	t.Helper()
	st := exFileStore(t, path)
	closeTimeout := 100 * time.Millisecond
	host := newHost(t, HostOptions{Store: st, OnError: func(HostError) {}, CloseTimeout: &closeTimeout})
	facet := DefineFacet(ssOwnerFacet(t, d, edits, behaviour, finished, func() Host { return host }))
	mustLoad(t)(host.Load([]HostFacet{{Name: "@fixture/owner", Facet: facet}}, background))
	return st, host
}

// --- proofs.ts -----------------------------------------------------------------------------------------------------

// ssContract is `contract(C)` of proofs.ts: a contract's params and, per version, its fingerprint and every member's
// normal form.
func ssContract(t testing.TB, contract *contracts.Contract) *delta.JsonObject {
	t.Helper()
	data, err := contracts.ContractDataOf(contract)
	must(t, err)
	versions := make([]any, len(data.Versions))
	for i, version := range data.Versions {
		variants := delta.NewJsonObject(version.Variants.Len())
		for name, variant := range version.Variants.All() {
			state, keyed, methods := delta.NewJsonObject(variant.State.Len()), delta.NewJsonObject(variant.Keyed.Len()), delta.NewJsonObject(variant.Methods.Len())
			for member, snapshot := range variant.State.All() {
				state.Set(member, snapshot.Normal)
			}
			for member, snapshot := range variant.Keyed.All() {
				keyed.Set(member, snapshot.Normal)
			}
			for member, method := range variant.Methods.All() {
				methods.Set(member, jsonObject("args", method.Args.Normal, "result", method.Result.Normal))
			}
			variants.Set(name, jsonObject("state", state, "keyed", keyed, "methods", methods))
		}
		versions[i] = jsonObject("fingerprint", version.Fingerprint, "variants", variants)
	}
	return jsonObject("params", data.Params.Normal, "versions", versions)
}

// ssTable is `table(T)` of proofs.ts.
func ssTable(t testing.TB, definition *store.TableDefinition) *delta.JsonObject {
	t.Helper()
	return jsonObject("fingerprint", definition.Fingerprint, "record", exJSON(t, exText(definition.Record)))
}

// ssFingerprints is `fingerprints()` of proofs.ts: every fingerprint and normal form of the declarations.
func ssFingerprints(t testing.TB, d *ssDeclarations) *delta.JsonObject {
	t.Helper()
	profileNormal, err := contracts.SnapshotSchema(d.everything, "p")
	must(t, err)
	return jsonObject(
		"contracts", jsonObject("everything", ssContract(t, d.Everything), "account", ssContract(t, d.Account), "owner", ssContract(t, d.Owner)),
		"documents", jsonObject("profile", jsonObject("fingerprint", d.Profile.Fingerprint, "normal", profileNormal.Normal),
			"notes", d.Notes.Fingerprint, "importState", d.importState.Fingerprint),
		"tables", jsonObject("rows", ssTable(t, d.Rows), "editsV1", ssTable(t, d.EditsV1), "editsV2", ssTable(t, d.EditsV2), "editsV3", ssTable(t, d.EditsV3)),
		"job", d.ImportJob.Fingerprint,
	)
}

// ssRead is `read(path, context)` of proofs.ts: the owner's dump on the store at path, after its stopped runs finished.
func ssRead(t *testing.T, d *ssDeclarations, path string) *delta.JsonObject {
	t.Helper()
	finished := &ssFinished{}
	st, host := ssOwnerHost(t, d, path, d.EditsV3, ssFinish, finished)
	owner := exGet(t, host, d.Owner, jsonObject())
	scopeEventually(t, "the three stopped runs finished", func() bool { return finished.len() >= 3 })
	dump := exCall(t, owner, "dump", jsonObject())
	result := jsonObject("dump", dump, "finished", finished.all(), "note", exCall(t, owner, "read", "b"))
	exCloseHost(t, host)
	exCloseStore(t, st)
	return result
}

// ssStoredReads is `storedReads(context)` of proofs.ts: what a host of the current code reads from the stored data:
// from a copy of the SQLite store, and from a store imported from the archive (both written at table version 1, read at
// version 3; the stopped job runs resume).
func ssStoredReads(t *testing.T, d *ssDeclarations) *delta.JsonObject {
	t.Helper()
	stored := filepath.Join("testdata", "schema-switch", "stored")
	directory := t.TempDir()
	content, err := os.ReadFile(filepath.Join(stored, "store.db"))
	must(t, err)
	fromStore := filepath.Join(directory, "store.db")
	must(t, os.WriteFile(fromStore, content, 0o600))
	storeRead := ssRead(t, d, fromStore)
	archive, err := os.ReadFile(filepath.Join(stored, "archive.jsonl"))
	must(t, err)
	database, err := node.OpenNodeSqliteDatabase(filepath.Join(directory, "imported.db"), node.NodeSqliteDatabaseOptions{})
	must(t, err)
	imported, err := store.ImportStore(store.SqliteBackend(database), store.DecodeJsonl(&exBytesSource{content: archive, chunk: 64 * 1024}), background, store.StoreOptions{})
	must(t, err)
	must(t, imported.Close(background))
	return jsonObject("store", storeRead, "archive", ssRead(t, d, filepath.Join(directory, "imported.db")))
}

// --- schema-switch.test.ts -----------------------------------------------------------------------------------------

// ssRecorded is `JSON.parse(readFileSync("./recorded.json", "utf8"))`.
func ssRecorded(t testing.TB) *delta.JsonObject {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "schema-switch", "recorded.json"))
	must(t, err)
	return exJSON(t, string(text)).(*delta.JsonObject)
}

// ssStructurallyEqual is vitest's `toEqual` over JSON values: the same members and elements, whatever the member order.
func ssStructurallyEqual(got, want any) bool {
	var left, right any
	if json.Unmarshal([]byte(exText(got)), &left) != nil || json.Unmarshal([]byte(exText(want)), &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// ssSameText fails unless got and want are the same JSON text, byte for byte (member order included).
func ssSameText(t testing.TB, what string, got, want any) {
	t.Helper()
	if g, w := exText(got), exText(want); g != w {
		t.Errorf("%s differs from Pi's recorded value:\n got  %s\n want %s", what, g, w)
	}
}

func ssChecksum(t testing.TB, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	must(t, err)
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestSchemaSwitchFixturesAreThoseOfPi(t *testing.T) {
	for path, want := range map[string]string{
		filepath.Join("testdata", "schema-switch", "stored", "store.db"):      ssStoreSHA256,
		filepath.Join("testdata", "schema-switch", "stored", "archive.jsonl"): ssArchiveSHA256,
		filepath.Join("testdata", "schema-switch", "recorded.json"):           ssRecordedSHA256,
	} {
		if got := ssChecksum(t, path); got != want {
			t.Errorf("%s: sha256 %s, want Pi's %s", path, got, want)
		}
	}
}

func TestSchemaSwitchFingerprints(t *testing.T) {
	t.Run("keeps every fingerprint and normal form of contracts, documents, tables, json columns and jobs", func(t *testing.T) {
		got, want := ssFingerprints(t, ssDeclared()), ssRecorded(t).Value("fingerprints").(*delta.JsonObject)
		if !ssStructurallyEqual(got, want) {
			t.Errorf("fingerprints differ from Pi's recorded values:\n got  %s\n want %s", exText(got), exText(want))
		}
		// The same values, byte for byte: every fingerprint, and every normal form, as the exact text Pi recorded.
		ssSameText(t, "fingerprints", got, want)
		for _, name := range []string{"everything", "account", "owner"} {
			ssSameText(t, "contract "+name, got.Value("contracts").(*delta.JsonObject).Value(name), want.Value("contracts").(*delta.JsonObject).Value(name))
		}
		for _, name := range []string{"rows", "editsV1", "editsV2", "editsV3"} {
			ssSameText(t, "table "+name, got.Value("tables").(*delta.JsonObject).Value(name), want.Value("tables").(*delta.JsonObject).Value(name))
		}
		ssSameText(t, "documents", got.Value("documents"), want.Value("documents"))
		ssSameText(t, "job", got.Value("job"), want.Value("job"))
	})
}

func TestSchemaSwitchStoredData(t *testing.T) {
	t.Run("reads a store and an archive written before the switch: documents, tables, a migrated table, stopped runs", func(t *testing.T) {
		got, want := ssStoredReads(t, ssDeclared()), ssRecorded(t).Value("stored").(*delta.JsonObject)
		if !ssStructurallyEqual(got, want) {
			t.Errorf("stored reads differ from Pi's recorded values:\n got  %s\n want %s", exText(got), exText(want))
		}
	})
}
