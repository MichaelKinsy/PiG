package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/files-copy.test.ts: copying a host's files (decision "Files v2"): a running host's
// `Files` index and blobs into a new host directory, nothing else of its store; the copy opens as a host of its own with
// every version, folder and summary, its blobs shared as links; an existing target is refused and a failed copy leaves
// nothing.

// notesFacet serves another object of the same store, whose document a copy of the files must leave behind.
func notesFacet(t *testing.T) (host.HostFacet, *contracts.Contract) {
	t.Helper()
	notes, err := contracts.Local("test.notes", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("text", contracts.Str()),
			contracts.Mem("write", contracts.NewMethod(contracts.Str())),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := documents.DefineState("notes", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("text", contracts.Str())), Version: 1, Persist: true,
		Initial:        func() *delta.JsonObject { return delta.JsonObjectOf("text", "") },
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			handle := self.(host.StoreSelf).State(doc).(*documents.Singleton)
			text := self.Derive([]any{handle}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("text"), nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"text": text},
				Methods: map[string]contracts.MethodFunction{
					"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := handle.Change(ctx, func(draft *delta.Object) (any, error) {
							_ = draft.Set("text", args)
							return nil, nil
						})
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
	return host.HostFacet{Name: "test.notes", Facet: facet}, notes
}

func blobNames(t *testing.T, dir string) []string {
	t.Helper()
	got := names(t, dir)
	sort.Strings(got)
	return got
}

func TestCopyCopiesARunningHostsIndexAndBlobsAndNothingElseOfItsStore(t *testing.T) {
	base := t.TempDir()
	facet, notes := notesFacet(t)
	source := filesOf(t, fixtureOptions{Dir: filepath.Join(base, "source"), Extra: []host.HostFacet{facet}})
	v1 := source.putIn("artifacts", "report", "one")
	v2 := source.putIn("artifacts", "report", "two")
	source.putIn("uploads", "docs/a.txt", "upload")
	source.must("copy", "id", str(v1, "id"), "space", "shared", "path", "copy-of-report")
	ref, err := source.host.Get(background, notes, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ref.Call("write", "not copied", background); err != nil {
		t.Fatal(err)
	}
	ref.Release()

	if err := CopyFiles(FilesSource{Store: source.store, Files: filepath.Join(base, "source", "files")}, filepath.Join(base, "copy"), background); err != nil {
		t.Fatal(err)
	}
	// The source goes on.
	source.putIn("artifacts", "report", "three")

	copyFacet, copyNotes := notesFacet(t)
	copied := filesOf(t, fixtureOptions{Dir: filepath.Join(base, "copy"), Extra: []host.HostFacet{copyFacet}})
	versions := copied.must("versions", "space", "artifacts", "path", "report").(*delta.JsonObject)
	var ids []string
	for _, item := range versions.Value("items").([]any) {
		ids = append(ids, str(item.(*delta.JsonObject), "id"))
	}
	if !reflect.DeepEqual(ids, []string{str(v2, "id"), str(v1, "id")}) {
		t.Fatalf("versions = %v", ids)
	}
	if got := copied.text(str(v1, "id")); got != "one" {
		t.Fatalf("v1 = %q", got)
	}
	if page := copied.must("list", "space", "uploads", "folder", "docs").(*delta.JsonObject); len(page.Value("items").([]any)) != 1 {
		t.Fatalf("uploads/docs = %s", jsonOf(t, page))
	}
	if summary := copied.summary("artifacts"); num(summary, "files") != 1 || num(summary, "versions") != 2 {
		t.Fatalf("summary = %s", jsonOf(t, summary))
	}
	// Blobs are shared files: the copy of `report` v1 in `shared` and v1 itself are one blob, linked once.
	blobs := blobNames(t, filepath.Join(base, "copy", "files", "blobs"))
	if len(blobs) != 3 {
		t.Fatalf("blobs = %v", blobs)
	}
	for _, blob := range blobs {
		a, _ := os.Stat(filepath.Join(base, "source", "files", "blobs", blob))
		b, _ := os.Stat(filepath.Join(base, "copy", "files", "blobs", blob))
		if !os.SameFile(a, b) {
			t.Fatalf("blob %s is not a link", blob)
		}
	}
	copiedNotes, err := copied.host.Get(background, copyNotes, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := copiedNotes.Member("text").Value(); text != "" {
		t.Fatalf("copied notes = %v, want empty", text)
	}
	copiedNotes.Release()
	// Removing in the copy leaves the source's blob.
	copied.mustRaw("removeSpace", "artifacts")
	if got := source.text(str(v1, "id")); got != "one" {
		t.Fatalf("source v1 = %q", got)
	}
	if errors := append(source.errors.all(), copied.errors.all()...); len(errors) != 0 {
		t.Fatalf("background errors = %v", errors)
	}
	copied.close()
	source.close()
}

func TestCopyRefusesATargetThatExistsAFailedCopyLeavesNothing(t *testing.T) {
	base := t.TempDir()
	source := filesOf(t, fixtureOptions{Dir: filepath.Join(base, "source")})
	file := source.putIn("s", "a", "a", "mime", "x")
	if err := os.Mkdir(filepath.Join(base, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	from := FilesSource{Store: source.store, Files: filepath.Join(base, "source", "files")}
	if err := CopyFiles(from, filepath.Join(base, "taken"), background); err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Fatalf("CopyFiles(taken) = %v", err)
	}
	// A blob gone from disk fails the copy: nothing is left beside the target.
	blobs := filepath.Join(base, "source", "files", "blobs")
	if err := os.Remove(filepath.Join(blobs, names(t, blobs)[0])); err != nil {
		t.Fatal(err)
	}
	if err := CopyFiles(from, filepath.Join(base, "copy"), background); err == nil {
		t.Fatal("CopyFiles with a missing blob did not fail")
	}
	if got := blobNames(t, base); !reflect.DeepEqual(got, []string{"source", "taken"}) {
		t.Fatalf("base = %v", got)
	}
	if str(file, "path") != "a" {
		t.Fatalf("file = %s", jsonOf(t, file))
	}
	source.close()
}
