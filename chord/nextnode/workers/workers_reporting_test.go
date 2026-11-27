package workers

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the server side of packages/chord/test/node/workers/workers.test.ts `reporting`: a server facet serving `Reports` into a
// `Journal` (persisted when asked), and what the tests of a worker's exit read of them.

// fixtureJournal is the server's journal of its workers' reports (`Journal`).
var fixtureJournal = mustFixture(contracts.Local("test.journal", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("lines", contracts.Arr(contracts.Str())), contracts.Mem("append", contracts.NewMethod(contracts.Str()))},
}))

func defineLines(options documents.StateOptions) *documents.StateDefinition {
	options.Schema = contracts.Obj(contracts.Prop("lines", contracts.Arr(contracts.Str())))
	options.Version = 1
	options.Initial = func() *delta.JsonObject { return delta.JsonObjectOf("lines", []any{}) }
	definition, err := documents.DefineState("lines", options)
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	fixtureLines       = defineLines(documents.StateOptions{})
	fixtureStoredLines = defineLines(documents.StateOptions{Persist: true, CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false }})
)

// reportLog is the reports a `reporting` facet received and how their transactions ended (`{ received, outcomes }`).
type reportLog struct {
	received collector[string]
	outcomes collector[string]
}

// reportOptions are the options of reporting.
type reportOptions struct {
	// persist keeps the journal's lines in the server's store.
	persist bool
	// before, when not nil, is waited for by each report before its transaction (`before`).
	before <-chan struct{}
	// seen, when not nil, adds what it says of the call's incarnation to the text a report is received with (`seen`).
	seen func(incarnation string) string
}

// reporting is a server facet serving `Reports` into `Journal`: a report is `received` (with what `seen` says of its call), waits
// for `before`, then appends its text in a transaction under its call's ctx; `outcomes` gets how that ended: `<text> committed`,
// or `<text>` and the error's code.
func reporting(log *reportLog, options reportOptions) host.HostFacet {
	lines := fixtureLines
	if options.persist {
		lines = fixtureStoredLines
	}
	return host.HostFacet{Name: "@test/reports", Facet: host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(fixtureJournal, func(_ *delta.JsonObject, self host.Self) (contracts.MembersImplementation, error) {
			document := self.State(lines).(*documents.Singleton)
			return contracts.MembersImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"lines": self.Derive([]any{document}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("lines"), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							_, err := draft.Array("lines").Push(args)
							return nil, err
						})
						return nil, err
					},
				},
			}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(fixtureReports, func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.MembersImplementation, error) {
			journal := self.Get(fixtureJournal, delta.NewJsonObject(0))
			return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
				"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					text, _ := args.(string)
					received := text
					if options.seen != nil {
						received += " " + options.seen(ctx.(host.Ctx).Incarnation())
					}
					log.received.add(received)
					if options.before != nil {
						// `await options.before`: a wait for a gate, which the call's abort does not end.
						chordctx.Suspend(ctx)
						<-options.before
					}
					outcome := text + " committed"
					if _, err := journal.Call("append", text, ctx); err != nil {
						code := "threw"
						if chordErr, ok := data.IsChordError(err); ok {
							code = string(chordErr.Code)
						}
						outcome = text + " " + code
					}
					log.outcomes.add(outcome)
					return nil, nil
				},
			}}, nil
		}, host.ServeOptions{})
	})}
}

// journalOf is the server's journal, held.
func (f *fleet) journalOf() *host.Reference {
	f.t.Helper()
	journal, err := f.server.Get(background, fixtureJournal, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	return journal
}

// linesOf is `journal.lines.value`.
func linesOf(t testing.TB, journal *host.Reference) []string {
	t.Helper()
	value, err := journal.Member("lines").Value()
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	lines := []string{}
	items, _ := value.([]any)
	for _, item := range items {
		lines = append(lines, item.(string))
	}
	return lines
}

// childOf is the child of session id as the component has it (a look into the component, under its loop): nil when none.
func (f *fleet) childOf(id string) *child {
	component := f.workers
	var found *child
	component.loop.Run(func() {
		if record := component.addresses["session|"+id]; record != nil {
			found = record.child
		}
	})
	return found
}

// handled waits until the component handled child's exit: a process's exit handler runs before anyone waiting on Exited resumes
// (workers_process.go), so what the component does at an exit without waiting for anything is done, and what it waits for is
// being waited for. The limit is a fault detector, not what the wait relies on.
func handled(t testing.TB, child *child) {
	t.Helper()
	select {
	case <-child.process.Exited():
	case <-time.After(30 * time.Second):
		t.Fatal("the worker did not exit")
	}
}

// reportsCall is `control.report(text, context)` of Pi (here on the reporter: fixtureReporter): the call returns once the worker sent the
// report.
func reportsCall(t testing.TB, reporter *host.Reference, method, text string) {
	t.Helper()
	if _, err := reporter.Call(method, text, background); err != nil {
		t.Fatalf("%s %q: %v", method, text, err)
	}
}
