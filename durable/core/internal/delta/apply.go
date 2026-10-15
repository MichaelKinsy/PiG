package delta

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Apply applies one batch without mutating target. The result shares
// unchanged containers with target and adopts operation payloads without
// copying them.
func Apply(target any, ops []Op) (any, error) {
	root := target
	r := replay{objects: map[*jv.Object]struct{}{}, arrays: map[*any]struct{}{}}
	for _, op := range ops {
		if op.Verb == Replace {
			root = op.Value
			continue
		}
		next, err := r.applyOne(root, op)
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

// replay records the containers this replay already copied, so repeated
// operations under one parent copy it once.
type replay struct {
	objects map[*jv.Object]struct{}
	arrays  map[*any]struct{}
}

func (r *replay) own(value any, full Path) (any, error) {
	switch c := value.(type) {
	case *jv.Object:
		if _, ok := r.objects[c]; ok {
			return c, nil
		}
		cp := c.Shallow()
		r.objects[cp] = struct{}{}
		return cp, nil
	case []any:
		if len(c) > 0 {
			if _, ok := r.arrays[&c[0]]; ok {
				return c, nil
			}
		}
		cp := make([]any, len(c), len(c)+1)
		copy(cp, c)
		if len(cp) > 0 {
			r.arrays[&cp[0]] = struct{}{}
		}
		return cp, nil
	}
	return nil, pathErr(full)
}

func (r *replay) applyOne(root any, op Op) (any, error) {
	if op.Verb == Splice || op.Verb == Permute {
		return r.updateAt(root, op.Path, op.Path, func(target any) (any, error) {
			items, ok := target.([]any)
			if !ok {
				return nil, pathErr(op.Path)
			}
			if op.Verb == Splice {
				return spliceItems(items, op), nil
			}
			if len(op.Perm) != len(items) {
				return nil, pathErr(op.Path)
			}
			out := make([]any, len(items))
			for i, from := range op.Perm {
				out[i] = items[from]
			}
			return out, nil
		})
	}
	if len(op.Path) == 0 {
		return nil, invalid("empty path")
	}
	parent, key := op.Path[:len(op.Path)-1], op.Path[len(op.Path)-1]
	return r.updateAt(root, parent, op.Path, func(p any) (any, error) { return applyLeaf(p, key, op) })
}

func (r *replay) updateAt(root any, path, full Path, update func(any) (any, error)) (any, error) {
	copied, err := r.own(root, full)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return update(copied)
	}
	seg := path[0]
	switch c := copied.(type) {
	case *jv.Object:
		if seg.IsIndex {
			return nil, pathErr(full)
		}
		child, present := c.Get(seg.Key)
		if !present {
			return nil, pathErr(full)
		}
		next, err := r.updateAt(child, path[1:], full, update)
		if err != nil {
			return nil, err
		}
		c.Set(seg.Key, next)
		return c, nil
	case []any:
		if !seg.IsIndex || seg.Index >= len(c) {
			return nil, pathErr(full)
		}
		next, err := r.updateAt(c[seg.Index], path[1:], full, update)
		if err != nil {
			return nil, err
		}
		c[seg.Index] = next
		return c, nil
	}
	return nil, pathErr(full)
}

func spliceItems(items []any, op Op) []any {
	start, remove := op.Index, op.Remove
	if start > len(items) {
		start = len(items)
	}
	if remove > len(items)-start {
		remove = len(items) - start
	}
	out := make([]any, 0, len(items)-remove+len(op.Items))
	out = append(out, items[:start]...)
	out = append(out, op.Items...)
	return append(out, items[start+remove:]...)
}

func applyLeaf(parent any, key Seg, op Op) (any, error) {
	switch c := parent.(type) {
	case *jv.Object:
		if key.IsIndex {
			return nil, pathErr(op.Path)
		}
		switch op.Verb {
		case Set:
			c.Set(key.Key, op.Value)
		case Delete:
			c.Delete(key.Key)
		case Append, Trim:
			cur, _ := c.Get(key.Key)
			s, ok := cur.(string)
			if !ok {
				return nil, pathErr(op.Path)
			}
			c.Set(key.Key, stringEdit(s, op))
		}
		return c, nil
	case []any:
		if !key.IsIndex || key.Index > len(c) {
			return nil, errors.Join(ErrUnsafePath, errors.New("index"))
		}
		i := key.Index
		switch op.Verb {
		case Set:
			if i == len(c) {
				return append(c, op.Value), nil
			}
			c[i] = op.Value
			return c, nil
		case Delete:
			if i >= len(c) {
				return nil, pathErr(op.Path)
			}
			return append(c[:i:i], c[i+1:]...), nil
		case Append, Trim:
			if i >= len(c) {
				return nil, pathErr(op.Path)
			}
			s, ok := c[i].(string)
			if !ok {
				return nil, pathErr(op.Path)
			}
			c[i] = stringEdit(s, op)
			return c, nil
		}
	}
	return nil, pathErr(op.Path)
}

func stringEdit(s string, op Op) string {
	if op.Verb == Append {
		return jv.Concat(s, op.Text)
	}
	return jv.SliceFrom(s, op.Count)
}
