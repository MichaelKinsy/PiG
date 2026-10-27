package typebox

// Ordered is a schema object read from JSON with its members in the order they were declared in, which TypeBox visits
// them in (properties, patternProperties, dependencies) and JSON.stringify writes them in. It keeps the members as a map
// as well, built once when the schema is decoded, so a reader looks a member up, or asks for its map, without copying.
// An Ordered is immutable after NewOrdered: it is safe for concurrent readers and is released with the schema holding it.
type Ordered struct {
	keys    []string
	members map[string]any
}

// NewOrdered is the object whose members are members, listed in keys order. Neither is changed afterwards by the caller.
func NewOrdered(keys []string, members map[string]any) *Ordered {
	return &Ordered{keys: keys, members: members}
}

// Keys are the members' names in declaration order. The slice is shared: do not change it.
func (o *Ordered) Keys() []string { return o.keys }

// Map is the members by name. The map is shared: do not change it.
func (o *Ordered) Map() map[string]any { return o.members }

// Get is the member under key.
func (o *Ordered) Get(key string) (any, bool) {
	value, ok := o.members[key]
	return value, ok
}

// Len is the number of members.
func (o *Ordered) Len() int { return len(o.keys) }
