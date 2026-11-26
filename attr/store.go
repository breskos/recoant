package attr

import (
	"fmt"
	"math"
	"slices"
	"sync"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/dict"
)

// column holds one attribute of all entities of a kind, by dense index.
type column struct {
	spec   Spec
	values *dict.Dictionary // category and set values
	codes  []uint32         // category: value index + 1; 0 is missing
	nums   []float64        // number: NaN is missing
	sets   [][]uint32       // set: sorted value indexes; nil is missing
	points []Point          // geo: NaN latitude is missing
	n      int              // entities with a value
}

func newColumn(spec Spec) *column {
	c := &column{spec: spec}
	if spec.Type == Category || spec.Type == Set {
		c.values = dict.New()
	}
	return c
}

func (c *column) has(i uint32) bool {
	switch c.spec.Type {
	case Category:
		return int(i) < len(c.codes) && c.codes[i] != 0
	case Number:
		return int(i) < len(c.nums) && !math.IsNaN(c.nums[i])
	case Set:
		return int(i) < len(c.sets) && c.sets[i] != nil
	default:
		return int(i) < len(c.points) && !math.IsNaN(c.points[i].Lat)
	}
}

func extend[T any](s []T, i uint32, missing T) []T {
	for len(s) <= int(i) {
		s = append(s, missing)
	}
	return s
}

func (c *column) set(i uint32, v Value) {
	if !c.has(i) {
		c.n++
	}
	switch c.spec.Type {
	case Category:
		c.codes = extend(c.codes, i, 0)
		c.codes[i] = c.values.Intern(v.Category) + 1
	case Number:
		c.nums = extend(c.nums, i, math.NaN())
		c.nums[i] = v.Number
	case Set:
		codes := make([]uint32, 0, len(v.Set))
		for _, e := range v.Set {
			codes = append(codes, c.values.Intern(e))
		}
		slices.Sort(codes)
		c.sets = extend(c.sets, i, nil)
		c.sets[i] = slices.Compact(codes)
	default:
		c.points = extend(c.points, i, Point{Lat: math.NaN()})
		c.points[i] = v.Geo
	}
}

func (c *column) unset(i uint32) bool {
	if !c.has(i) {
		return false
	}
	c.n--
	switch c.spec.Type {
	case Category:
		c.codes[i] = 0
	case Number:
		c.nums[i] = math.NaN()
	case Set:
		c.sets[i] = nil
	default:
		c.points[i] = Point{Lat: math.NaN()}
	}
	return true
}

func (c *column) get(i uint32) (Value, bool) {
	if !c.has(i) {
		return Value{}, false
	}
	switch c.spec.Type {
	case Category:
		return Cat(c.values.ID(c.codes[i] - 1)), true
	case Number:
		return Num(c.nums[i]), true
	case Set:
		set := make([]string, len(c.sets[i]))
		for k, code := range c.sets[i] {
			set[k] = c.values.ID(code)
		}
		return Tags(set...), true
	default:
		return Value{Type: Geo, Geo: c.points[i]}, true
	}
}

type kindStore struct {
	dict *dict.Dictionary
	cols map[string]*column
}

// Store holds the attributes of entities. Create it with [New]. It is safe
// for concurrent use.
type Store struct {
	mu     sync.RWMutex
	schema Schema
	kinds  map[recoant.Kind]*kindStore
}

// New returns an empty store for a schema.
func New(schema Schema) (*Store, error) {
	if err := schema.Validate(); err != nil {
		return nil, err
	}
	s := &Store{schema: schema, kinds: make(map[recoant.Kind]*kindStore, len(schema))}
	for kind, attrs := range schema {
		ks := &kindStore{dict: dict.New(), cols: make(map[string]*column, len(attrs))}
		for name, spec := range attrs {
			ks.cols[name] = newColumn(spec)
		}
		s.kinds[kind] = ks
	}
	return s, nil
}

// Schema returns the schema. It must not be modified.
func (s *Store) Schema() Schema { return s.schema }

func (s *Store) column(ref recoant.Ref, name string) (*kindStore, *column, error) {
	if err := ref.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	ks := s.kinds[ref.Kind]
	if ks == nil || ks.cols[name] == nil {
		return nil, nil, fmt.Errorf("%w: %s.%s", ErrUnknown, ref.Kind, name)
	}
	return ks, ks.cols[name], nil
}

// Set sets an attribute of an entity, replacing its value.
func (s *Store) Set(ref recoant.Ref, name string, v Value) error {
	ks, c, err := s.column(ref, name)
	if err != nil {
		return err
	}
	if err := v.Validate(c.spec.Type); err != nil {
		return fmt.Errorf("%s.%s: %w", ref.Kind, name, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.set(ks.dict.Intern(ref.ID), v)
	return nil
}

// Unset removes an attribute of an entity. It reports whether the entity
// had it.
func (s *Store) Unset(ref recoant.Ref, name string) (bool, error) {
	ks, c, err := s.column(ref, name)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := ks.dict.Lookup(ref.ID)
	return ok && c.unset(i), nil
}

// Delete removes all attributes of an entity and forgets its ID. It
// reports whether the entity was known.
func (s *Store) Delete(ref recoant.Ref) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.kinds[ref.Kind]
	if ks == nil {
		return false
	}
	i, ok := ks.dict.Delete(ref.ID)
	if !ok {
		return false
	}
	for _, c := range ks.cols {
		c.unset(i)
	}
	return true
}

// Apply applies an event.
func (s *Store) Apply(ev Event) error {
	switch ev.Op {
	case "", OpSet:
		return s.Set(ev.Ref, ev.Attribute, ev.Value)
	case OpDelete:
		if ev.Attribute == "" {
			if err := ev.Ref.Validate(); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalid, err)
			}
			s.Delete(ev.Ref)
			return nil
		}
		_, err := s.Unset(ev.Ref, ev.Attribute)
		return err
	}
	return fmt.Errorf("%w: unknown op %q", ErrInvalid, ev.Op)
}

// Get returns an attribute of an entity.
func (s *Store) Get(ref recoant.Ref, name string) (Value, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.get(ref, name)
}

func (s *Store) get(ref recoant.Ref, name string) (Value, bool) {
	ks := s.kinds[ref.Kind]
	if ks == nil || ks.cols[name] == nil {
		return Value{}, false
	}
	i, ok := ks.dict.Lookup(ref.ID)
	if !ok {
		return Value{}, false
	}
	return ks.cols[name].get(i)
}

// Attributes returns the attributes of an entity, for profiles and
// exports. Sensitive attributes are left out unless includeSensitive is
// set.
func (s *Store) Attributes(ref recoant.Ref, includeSensitive bool) map[string]Value {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks := s.kinds[ref.Kind]
	if ks == nil {
		return nil
	}
	i, ok := ks.dict.Lookup(ref.ID)
	if !ok {
		return nil
	}
	var out map[string]Value
	for name, c := range ks.cols {
		if c.spec.Sensitive && !includeSensitive {
			continue
		}
		if v, ok := c.get(i); ok {
			if out == nil {
				out = make(map[string]Value)
			}
			out[name] = v
		}
	}
	return out
}

// Count returns the number of entities of a kind that have the attribute,
// or, for an empty name, the number of entities with any attribute record.
func (s *Store) Count(kind recoant.Kind, name string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks := s.kinds[kind]
	if ks == nil {
		return 0
	}
	if name == "" {
		return ks.dict.Len() - ks.dict.Tombstones()
	}
	if c := ks.cols[name]; c != nil {
		return c.n
	}
	return 0
}
