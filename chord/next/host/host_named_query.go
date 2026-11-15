package host

import (
	"context"
	"errors"
	"sort"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/host/named-query.ts (slice 14 §3.1): what `self.queries(table, options, builders)` returns, one
// OwnQuery per name. A reader asks the provider's declaration, checks its arguments, builds the query and reads it through
// the store.

// PartnerRow is a row of a joined set as a mapping gets it: the projected columns, decoded.
type PartnerRow map[string]any

// QueriesOptions is what `self.queries` takes besides the table and the builders.
type QueriesOptions struct {
	// Map turns a row (and, for a joined query, its partner) into the item readers get; nil: the row itself.
	Map func(row store.Row, partner PartnerRow) (delta.JsonValue, error)
	// Reads are the columns of the table Map gets: only these (a change of another is no change of the item); nil: all.
	Reads []string
	// PartnerReads are the columns of a joined set Map gets; nil: all.
	PartnerReads []string
}

// QueryBuilder is a named query's builder: a pure function of the query's arguments. Args is the schema of its arguments
// for a query private to its owner (Pi's `[argsSchema, builder]`); nil for a query the contract lists (the zero Schema; Pi's plain function:
// the contract has the argument schema).
type QueryBuilder struct {
	Build func(args delta.JsonValue) *store.Query
	Args  contracts.Schema
}

// OwnQuery is a named query as its owner declared it: the table, the builder, the schema its arguments are checked by (the
// contract's when the query is listed there, its own otherwise), the columns its mapping gets and the mapping.
type OwnQuery struct {
	label      string
	Handle     *store.TableHandle
	builder    func(args delta.JsonValue) *store.Query
	projection store.Projection
	mapper     func(row store.Row, partner PartnerRow) (delta.JsonValue, error)
	// args is the schema of its arguments: its own (a private query), or the contract's once its member was checked.
	args   *contracts.Snapshot
	listed bool
}

var _ contracts.NamedQuery[delta.JsonValue, delta.JsonValue] = (*OwnQuery)(nil)

func newOwnQuery(owner, name string, handle *store.TableHandle, builder func(args delta.JsonValue) *store.Query, options QueriesOptions, args *contracts.Snapshot) *OwnQuery {
	return &OwnQuery{
		label:      owner + `: query "` + name + `"`,
		Handle:     handle,
		builder:    builder,
		projection: store.Projection{Reads: options.Reads, PartnerReads: options.PartnerReads},
		mapper:     options.Map,
		args:       args,
	}
}

// Checked is whether its arguments have a schema: its own, or the contract's.
func (q *OwnQuery) Checked() bool { return q.args != nil }

// Listed is the member check's: the contract lists this query with the argument schema args; a listed query brings no
// schema of its own (declaration policy).
func (q *OwnQuery) Listed(args *contracts.Snapshot) error {
	if q.listed {
		return nil
	}
	if q.args != nil {
		return errors.New(q.label + ": the contract has its argument schema; give only the builder")
	}
	q.args = args
	q.listed = true
	return nil
}

// Page implements contracts.NamedQuery: one page, read once, not live.
func (q *OwnQuery) Page(args delta.JsonValue, limit int64, ctx context.Context, options contracts.QueryPageOptions) (contracts.QueryPage[delta.JsonValue], error) {
	return q.PageOf(args, limit, ctx, options.After)
}

// Target is what reading this query for args reads: args checked (a trust boundary), the query built and checked, and how
// a stored row of it decodes.
func (q *OwnQuery) Target(args delta.JsonValue, ctx context.Context) (store.LiveTarget, error) {
	query, err := q.build(args, ctx)
	if err != nil {
		return store.LiveTarget{}, err
	}
	handle := q.Handle
	return store.LiveTarget{
		Spec: store.QuerySpec{Query: query, Table: handle.Sql.Table, Projection: q.projection, Owner: handle},
		Decode: func(stored store.StoredRead, decoding context.Context) (delta.JsonValue, error) {
			return q.decode(query, stored, decoding)
		},
	}, nil
}

// PageOf is up to limit items for args after the cursor after, and the cursor of the next page when there is one.
func (q *OwnQuery) PageOf(args delta.JsonValue, limit int64, ctx context.Context, after *string) (contracts.QueryPage[delta.JsonValue], error) {
	handle := q.Handle
	if err := handle.Owner.AssertLive(); err != nil {
		return contracts.QueryPage[delta.JsonValue]{}, err
	}
	query, err := q.build(args, ctx)
	if err != nil {
		return contracts.QueryPage[delta.JsonValue]{}, err
	}
	plan, err := store.NewReadPlan(query, handle.Sql.Table, q.projection, store.ReadRows, handle.Store.Reads)
	if err != nil {
		return contracts.QueryPage[delta.JsonValue]{}, err
	}
	page, err := store.ReadPage(handle.Store, plan, limit, after, ctx)
	if err != nil {
		return contracts.QueryPage[delta.JsonValue]{}, err
	}
	items := make([]delta.JsonValue, 0, len(page.Stored))
	for _, row := range page.Stored {
		item, err := q.decode(query, row, ctx)
		if err != nil {
			return contracts.QueryPage[delta.JsonValue]{}, err
		}
		items = append(items, item)
	}
	return contracts.QueryPage[delta.JsonValue]{Items: items, Next: page.Next}, nil
}

// build is what reading this query for args reads: args checked (a trust boundary), the query built and checked.
func (q *OwnQuery) build(args delta.JsonValue, ctx context.Context) (*store.Query, error) {
	schema := q.args
	// Declaration policy: a query the contract does not list brings its schema (`name: {Args, Build}`).
	if schema == nil {
		return nil, errors.New(q.label + ": a private query brings its argument schema")
	}
	validator, err := data.ValidatorFor(schema, ctx)
	if err != nil {
		return nil, err
	}
	if err := data.Check(validator, args, data.ReasonArgs, ctx); err != nil {
		return nil, err
	}
	// The builder is plugin code: it may throw (panic), what it returns is checked.
	var built *store.Query
	if _, err := tryPlugin(func() (any, error) { built = q.builder(args); return nil, nil }); err != nil {
		return nil, err
	}
	if built == nil || built.Table != q.Handle.Definition {
		return nil, errors.New(q.label + `: the builder returns a query of table "` + q.Handle.Definition.Name + `"`)
	}
	if err := built.Err(); err != nil {
		return nil, err
	}
	return built, nil
}

// decode is a stored row as readers get it: the row (and partner) decoded and mapped. The statement also selects what the
// store needs (the key, the order and match columns): the mapping gets only the columns Reads and PartnerReads list, so a
// column it does not declare is absent, at once and visibly.
func (q *OwnQuery) decode(query *store.Query, stored store.StoredRead, ctx context.Context) (delta.JsonValue, error) {
	reads, partnerReads := q.projection.Reads, q.projection.PartnerReads
	definition := q.Handle.Definition
	own := stored
	if reads != nil {
		own = projected(stored, reads)
	}
	// A table's rows hold its columns' values (scalars and JSON); a mapping's result is the item (its contract's).
	if q.mapper == nil {
		return store.DecodedRowJSON(definition, own, ctx)
	}
	row, err := definition.Rows.Decode(ctx, own)
	if err != nil {
		return nil, err
	}
	partner := PartnerRow{}
	if joined := query.Joined; joined != nil {
		partner, err = partnerRow(joined.Set, partnerReads, stored)
		if err != nil {
			return nil, err
		}
	}
	return q.mapper(row, partner)
}

// projected is the columns names of stored, each `json(...)` one with its version column.
func projected(stored store.StoredRead, names []string) store.StoredRead {
	row := make(store.StoredRead, len(names))
	for _, name := range names {
		row[name] = stored[name]
		if version, present := stored[store.VersionColumn(name)]; present {
			row[store.VersionColumn(name)] = version
		}
	}
	return row
}

// partnerRow is the joined set's columns names (all without) of stored (named `<set>:<column>` there), decoded.
func partnerRow(set *store.ColumnSet, names []string, stored store.StoredRead) (PartnerRow, error) {
	if names == nil {
		for _, column := range set.Columns {
			names = append(names, column.Name)
		}
	}
	row := PartnerRow{}
	for _, name := range names {
		value := stored[store.PartnerName(set.Name, name)]
		// A set's columns are scalar (`DefineColumns` refuses `json(...)`).
		if value == nil {
			continue
		}
		decoded, err := store.DecodeScalar(set.ByName[name], value)
		if err != nil {
			return nil, err
		}
		row[name] = decoded
	}
	return row, nil
}

// ownQueries is the named queries of value when it is what `self.queries` returned: a map of OwnQuerys (a nil map or a
// foreign NamedQuery is not).
func ownQueries(value any) (map[string]*OwnQuery, bool) {
	given, ok := value.(map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue])
	if !ok || given == nil {
		return nil, false
	}
	queries := make(map[string]*OwnQuery, len(given))
	for name, each := range given {
		own, ok := each.(*OwnQuery)
		if !ok {
			return nil, false
		}
		queries[name] = own
	}
	return queries, true
}

// ownQueryNames is the names of queries in order: a refusal names the first.
func ownQueryNames(queries map[string]*OwnQuery) []string {
	names := make([]string, 0, len(queries))
	for name := range queries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NamedQueryHandle is a named query as readers hold it: its owner's declaration (OwnQuery), or a reference's member
// (ReferenceQuery).
type NamedQueryHandle interface {
	contracts.NamedQuery[delta.JsonValue, delta.JsonValue]
	// Label is the query in errors and reports: its name, with its member for a reference's.
	Label() string
	// Provided is the provider's declaration; an error while the provider is not live (a reference's state says why).
	Provided() (*OwnQuery, error)
	// Follow calls moved whenever another instance of the provider is live, until the returned release.
	Follow(moved func()) (release func())
}

// Label implements NamedQueryHandle.
func (q *OwnQuery) Label() string { return q.label }

// Provided implements NamedQueryHandle: itself.
func (q *OwnQuery) Provided() (*OwnQuery, error) { return q, nil }

// Follow implements NamedQueryHandle: an own query's provider is the one declaring it, which never moves.
func (q *OwnQuery) Follow(func()) (release func()) { return func() {} }

// ReferenceQuery is a named query of a reference's `queries` member: the provider's declaration of that name, while it is
// live.
type ReferenceQuery struct {
	binding *binding
	member  string
	name    string
}

var _ NamedQueryHandle = (*ReferenceQuery)(nil)

// Label implements NamedQueryHandle: read on the host's loop, where the binding's state lives.
func (q *ReferenceQuery) Label() string {
	var label string
	q.binding.loop.Run(func() { label = q.binding.label() })
	return label + `: query "` + q.member + "." + q.name + `"`
}

// Follow implements NamedQueryHandle: its binding tells of every other live instance of the provider, on the host's loop.
func (q *ReferenceQuery) Follow(moved func()) (release func()) {
	var unfollow func()
	q.binding.loop.Run(func() { unfollow = q.binding.followInstance(moved) })
	return func() { q.binding.loop.Run(unfollow) }
}

// Provided implements NamedQueryHandle: the provider's declaration, resolved on the host's loop as a keyed member's slot is.
func (q *ReferenceQuery) Provided() (*OwnQuery, error) {
	var own *OwnQuery
	var err error
	q.binding.loop.Run(func() { own, err = q.binding.queryMember(q.member, q.name) })
	return own, err
}

// Page implements contracts.NamedQuery: up to limit items for args (after options.After) of the provider's query.
func (q *ReferenceQuery) Page(args delta.JsonValue, limit int64, ctx context.Context, options contracts.QueryPageOptions) (contracts.QueryPage[delta.JsonValue], error) {
	own, err := q.Provided()
	if err != nil {
		return contracts.QueryPage[delta.JsonValue]{}, err
	}
	return own.PageOf(args, limit, ctx, options.After)
}
