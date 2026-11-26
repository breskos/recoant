package attr

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/breskos/recoant"
)

var inf = math.Inf(1)

// test checks one candidate index of the target kind.
type test func(i uint32) bool

// Check reports an error if the conditions do not fit the store's schema;
// see [Schema.Check].
func (s *Store) Check(subject, target recoant.Kind, conds []recoant.Condition) error {
	return s.schema.Check(subject, target, conds)
}

// Check reports an error if the conditions do not fit the schema for
// requests from subjects of kind subject for candidates of kind target.
func (s Schema) Check(subject, target recoant.Kind, conds []recoant.Condition) error {
	for _, c := range conds {
		if err := s.check(subject, target, c); err != nil {
			return err
		}
	}
	return nil
}

// NeedsSubject reports whether a condition refers to the subject's
// attributes.
func NeedsSubject(c recoant.Condition) bool {
	switch c.Test() {
	case "within", "same":
		return true
	case "distance":
		return c.Near == nil
	}
	return false
}

func (s Schema) check(subject, target recoant.Kind, c recoant.Condition) error {
	if err := c.Validate(); err != nil {
		return err
	}
	want := func(kind recoant.Kind, name string, types ...Type) error {
		spec, ok := s.Spec(kind, name)
		if !ok {
			return fmt.Errorf("%w: %s: unknown attribute %s.%s", ErrCondition, c.Attribute, kind, name)
		}
		if !slices.Contains(types, spec.Type) {
			return fmt.Errorf("%w: %s: %s.%s is a %s attribute, %s needs %v", ErrCondition, c.Attribute, kind, name, spec.Type, c.Test(), types)
		}
		return nil
	}
	switch c.Test() {
	case "in":
		return want(target, c.Attribute, Category, Set)
	case "range":
		return want(target, c.Attribute, Number)
	case "distance":
		if err := want(target, c.Attribute, Geo); err != nil || c.Near != nil {
			return err
		}
		return want(subject, c.Attribute, Geo)
	case "within":
		for _, err := range []error{want(target, c.Attribute, Number), want(subject, c.Within[0], Number), want(subject, c.Within[1], Number)} {
			if err != nil {
				return err
			}
		}
		if !c.Mutual {
			return nil
		}
		for _, err := range []error{want(subject, c.Attribute, Number), want(target, c.Within[0], Number), want(target, c.Within[1], Number)} {
			if err != nil {
				return err
			}
		}
		return nil
	default: // same
		spec, _ := s.Spec(target, c.Attribute)
		if err := want(target, c.Attribute, Category, Set); err != nil {
			return err
		}
		return want(subject, c.Attribute, spec.Type)
	}
}

// Compile turns conditions into a filter for the candidates of a request
// from subject for the target kind. Values of the subject are read now;
// candidates are read when the filter is called. Conditions that refer to
// attributes the subject lacks are skipped; if all are skipped, the filter
// accepts every candidate. The filter is safe for concurrent use.
func (s *Store) Compile(subject recoant.Ref, target recoant.Kind, conds []recoant.Condition) (func(recoant.Ref) bool, error) {
	if err := s.Check(subject.Kind, target, conds); err != nil {
		return nil, err
	}
	s.mu.RLock()
	ks := s.kinds[target]
	var tests []test
	for _, c := range conds {
		if t := s.compile(subject, ks, c); t != nil {
			tests = append(tests, t)
		}
	}
	s.mu.RUnlock()
	if len(tests) == 0 {
		return func(recoant.Ref) bool { return true }, nil
	}
	return func(ref recoant.Ref) bool {
		if ref.Kind != target {
			return false
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		i, ok := ks.dict.Lookup(ref.ID)
		if !ok {
			return false
		}
		for _, t := range tests {
			if !t(i) {
				return false
			}
		}
		return true
	}, nil
}

// compile returns the test of a checked condition, or nil if it is
// skipped. It is called with the read lock held; tests are called with it
// held as well.
func (s *Store) compile(subject recoant.Ref, ks *kindStore, c recoant.Condition) test {
	col := ks.cols[c.Attribute]
	num := func(name string) (float64, bool) {
		v, ok := s.get(subject, name)
		return v.Number, ok
	}
	switch c.Test() {
	case "in":
		var codes []uint32
		for _, v := range c.In {
			if code, ok := col.values.Lookup(v); ok {
				codes = append(codes, code)
			}
		}
		return inTest(col, codes)
	case "range":
		lo, hi := -inf, inf
		if c.Min != nil {
			lo = *c.Min
		}
		if c.Max != nil {
			hi = *c.Max
		}
		return func(i uint32) bool { return col.has(i) && col.nums[i] >= lo && col.nums[i] <= hi }
	case "distance":
		var center Point
		if c.Near != nil {
			center = Point{Lat: c.Near[0], Lon: c.Near[1]}
		} else {
			v, ok := s.get(subject, c.Attribute)
			if !ok {
				return nil
			}
			center = v.Geo
		}
		return func(i uint32) bool { return col.has(i) && DistanceKm(center, col.points[i]) <= c.MaxDistanceKm }
	case "within":
		lo, okLo := num(c.Within[0])
		hi, okHi := num(c.Within[1])
		own, okOwn := num(c.Attribute)
		forward := okLo && okHi
		backward := c.Mutual && okOwn
		if !forward && !backward {
			return nil
		}
		cLo, cHi := ks.cols[c.Within[0]], ks.cols[c.Within[1]]
		return func(i uint32) bool {
			if forward && !(col.has(i) && col.nums[i] >= lo && col.nums[i] <= hi) {
				return false
			}
			if backward && cLo.has(i) && cHi.has(i) && !(own >= cLo.nums[i] && own <= cHi.nums[i]) {
				return false
			}
			return true
		}
	default: // same
		v, ok := s.get(subject, c.Attribute)
		if !ok {
			return nil
		}
		vals := v.Set
		if v.Type == Category {
			vals = []string{v.Category}
		}
		var codes []uint32
		for _, x := range vals {
			if code, ok := col.values.Lookup(x); ok {
				codes = append(codes, code)
			}
		}
		return inTest(col, codes)
	}
}

// inTest accepts candidates whose category is one of codes or whose set
// overlaps them.
func inTest(col *column, codes []uint32) test {
	slices.Sort(codes)
	if col.spec.Type == Category {
		return func(i uint32) bool {
			if !col.has(i) {
				return false
			}
			_, ok := slices.BinarySearch(codes, col.codes[i]-1)
			return ok
		}
	}
	return func(i uint32) bool {
		if !col.has(i) {
			return false
		}
		for _, code := range col.sets[i] {
			if _, ok := slices.BinarySearch(codes, code); ok {
				return true
			}
		}
		return false
	}
}

type filtered struct {
	rec   recoant.Recommender
	attrs *Store
}

// Filtered wraps a recommender so that it answers requests with attribute
// conditions ([recoant.Request.Where]): the conditions are compiled into
// the request's Filter (combined with an existing one) before the
// recommender sees the request.
func Filtered(r recoant.Recommender, s *Store) recoant.Recommender { return filtered{rec: r, attrs: s} }

func (f filtered) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if len(req.Where) == 0 {
		return f.rec.Recommend(ctx, req)
	}
	filter, err := f.attrs.Compile(req.Subject, req.Target, req.Where)
	if err != nil {
		return nil, err
	}
	if prev := req.Filter; prev != nil {
		req.Filter = func(r recoant.Ref) bool { return prev(r) && filter(r) }
	} else {
		req.Filter = filter
	}
	req.Where = nil
	return f.rec.Recommend(ctx, req)
}
