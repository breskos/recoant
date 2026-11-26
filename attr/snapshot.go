package attr

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/dict"
)

// SnapshotVersion is the version of the [Snapshot] layout.
const SnapshotVersion = 1

// ErrInvalidSnapshot is returned by [FromSnapshot] for inconsistent data.
var ErrInvalidSnapshot = errors.New("attr: invalid snapshot")

// Snapshot is the serializable content of a store. It holds sensitive
// attributes as well: it is for storage, not for export.
type Snapshot struct {
	Version int            `json:"version"`
	Schema  Schema         `json:"schema"`
	Kinds   []KindSnapshot `json:"kinds"`
}

// KindSnapshot holds the entities of a kind in index order (deleted ones
// have an empty ID) and their attributes.
type KindSnapshot struct {
	Kind    recoant.Kind     `json:"kind"`
	IDs     []string         `json:"ids"`
	Columns []ColumnSnapshot `json:"columns,omitempty"`
}

// ColumnSnapshot holds the values of an attribute: Index lists the
// entities that have a value; the field of the attribute's type holds the
// values in the same order. Category and set values are indexes into
// Values.
type ColumnSnapshot struct {
	Name    string     `json:"name"`
	Index   []uint32   `json:"index"`
	Values  []string   `json:"values,omitempty"`
	Codes   []uint32   `json:"codes,omitempty"`
	Numbers []float64  `json:"numbers,omitempty"`
	Sets    [][]uint32 `json:"sets,omitempty"`
	Points  []Point    `json:"points,omitempty"`
}

// Snapshot returns a copy of the store's content.
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sn := &Snapshot{Version: SnapshotVersion, Schema: s.schema}
	for kind, ks := range s.kinds {
		k := KindSnapshot{Kind: kind, IDs: slices.Clone(ks.dict.IDs())}
		for name, c := range ks.cols {
			cs := ColumnSnapshot{Name: name, Index: []uint32{}}
			if c.values != nil {
				cs.Values = slices.Clone(c.values.IDs())
			}
			for i := range uint32(ks.dict.Len()) {
				if !c.has(i) {
					continue
				}
				cs.Index = append(cs.Index, i)
				switch c.spec.Type {
				case Category:
					cs.Codes = append(cs.Codes, c.codes[i]-1)
				case Number:
					cs.Numbers = append(cs.Numbers, c.nums[i])
				case Set:
					cs.Sets = append(cs.Sets, slices.Clone(c.sets[i]))
				default:
					cs.Points = append(cs.Points, c.points[i])
				}
			}
			k.Columns = append(k.Columns, cs)
		}
		slices.SortFunc(k.Columns, func(a, b ColumnSnapshot) int { return cmp.Compare(a.Name, b.Name) })
		sn.Kinds = append(sn.Kinds, k)
	}
	slices.SortFunc(sn.Kinds, func(a, b KindSnapshot) int { return cmp.Compare(a.Kind, b.Kind) })
	return sn
}

// FromSnapshot restores a store. The snapshot must not be modified
// afterwards.
func FromSnapshot(sn *Snapshot) (*Store, error) {
	if sn == nil || sn.Version != SnapshotVersion {
		return nil, fmt.Errorf("%w: missing or unsupported version", ErrInvalidSnapshot)
	}
	s, err := New(sn.Schema)
	if err != nil {
		return nil, err
	}
	seen := make(map[recoant.Kind]bool)
	for _, k := range sn.Kinds {
		ks := s.kinds[k.Kind]
		if ks == nil || seen[k.Kind] {
			return nil, fmt.Errorf("%w: kind %q unknown or duplicate", ErrInvalidSnapshot, k.Kind)
		}
		seen[k.Kind] = true
		d, ok := dict.FromIDs(k.IDs)
		if !ok {
			return nil, fmt.Errorf("%w: duplicate id in kind %q", ErrInvalidSnapshot, k.Kind)
		}
		ks.dict = d
		for _, cs := range k.Columns {
			c := ks.cols[cs.Name]
			if c == nil || c.n > 0 {
				return nil, fmt.Errorf("%w: column %s.%s unknown or duplicate", ErrInvalidSnapshot, k.Kind, cs.Name)
			}
			if err := c.restore(cs, d); err != nil {
				return nil, fmt.Errorf("%w: column %s.%s: %w", ErrInvalidSnapshot, k.Kind, cs.Name, err)
			}
		}
	}
	return s, nil
}

func (c *column) restore(cs ColumnSnapshot, d *dict.Dictionary) error {
	n := len(cs.Index)
	if c.values != nil {
		values, ok := dict.FromIDs(cs.Values)
		if !ok || values.Tombstones() > 0 {
			return errors.New("invalid values")
		}
		c.values = values
	}
	var lens = map[Type]int{Category: len(cs.Codes), Number: len(cs.Numbers), Set: len(cs.Sets), Geo: len(cs.Points)}
	if lens[c.spec.Type] != n {
		return errors.New("inconsistent lengths")
	}
	for k, i := range cs.Index {
		if int(i) >= d.Len() || d.Deleted(i) || c.has(i) {
			return fmt.Errorf("invalid entity index %d", i)
		}
		var v Value
		switch c.spec.Type {
		case Category:
			if int(cs.Codes[k]) >= c.values.Len() {
				return errors.New("invalid value index")
			}
			v = Cat(c.values.ID(cs.Codes[k]))
		case Number:
			v = Num(cs.Numbers[k])
		case Set:
			v = Value{Type: Set}
			for _, code := range cs.Sets[k] {
				if int(code) >= c.values.Len() {
					return errors.New("invalid value index")
				}
				v.Set = append(v.Set, c.values.ID(code))
			}
			if v.Set == nil {
				v.Set = []string{}
			}
		default:
			v = Value{Type: Geo, Geo: cs.Points[k]}
		}
		if err := v.Validate(c.spec.Type); err != nil {
			return errors.New("invalid value")
		}
		c.set(i, v)
	}
	return nil
}
