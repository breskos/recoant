// Package segment groups entities into segments and describes them, so that
// users (or products) can be understood: who is in which segment, what a
// segment likes, and how an individual user compares.
//
// [Segmenter] clusters the entities of one kind with k-means on features:
// the taste or appearance vectors of a factor model and attributes
// (categories and sets one-hot, numbers and locations standardized). Every
// feature block is scaled to the same average size and then weighted, so
// that no block dominates by its number of dimensions. Sensitive
// attributes are used only when allowed explicitly.
//
// After Fit, [Segmenter.Segment] tells the segment of an entity (new
// entities are assigned to the nearest center), [Segmenter.Profiles]
// describes every segment and [Segmenter.Profile] one entity.
package segment

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/store"
)

// Feature names for the vectors of a factor model.
const (
	Taste      = "taste"      // subject vector: what the entity is drawn to
	Appearance = "appearance" // object vector: how the entity is perceived
)

// Roles gives the vectors of a factor model; all factor models of recoant
// implement it.
type Roles interface {
	Taste(ref recoant.Ref) ([]float64, bool)
	Appearance(ref recoant.Ref) ([]float64, bool)
}

// Feature is one input of the clustering.
type Feature struct {
	// Name is Taste, Appearance or an attribute of the kind.
	Name string `json:"name"`
	// Weight scales the feature block; default 1.
	Weight float64 `json:"weight,omitempty"`
}

// Config configures a Segmenter.
type Config struct {
	KMeansConfig
	// Kind is the kind of the entities to segment.
	Kind recoant.Kind `json:"kind"`
	// Features are the inputs of the clustering.
	Features []Feature `json:"features"`
	// Relation is used for the activity, the negative share and the
	// typical objects of segments and entities; its subject kind must be
	// Kind. Empty leaves them out.
	Relation store.Relation `json:"relation,omitzero"`
	// AllowSensitive allows sensitive attributes as features and in
	// profiles.
	AllowSensitive bool `json:"allow_sensitive,omitempty"`
	// Ignore lists attributes that profiles leave out, e.g. the one that
	// stores the segments.
	Ignore []string `json:"ignore,omitempty"`
}

// Segmenter clusters entities of one kind into segments. It is safe for
// concurrent use.
type Segmenter struct {
	store *store.Store
	attrs *attr.Store
	roles Roles
	cfg   Config

	mu  sync.RWMutex
	fit *fitted
}

// fitted is the state after Fit.
type fitted struct {
	enc      []encoder
	centers  [][]float64
	refs     []recoant.Ref // segmented entities
	points   [][]float64
	assign   []int
	index    map[recoant.Ref]int // position in refs
	profiles []Profile
}

// New returns an unfitted Segmenter. roles may be nil if no feature is
// Taste or Appearance.
func New(s *store.Store, attrs *attr.Store, roles Roles, cfg Config) (*Segmenter, error) {
	if s == nil || attrs == nil {
		return nil, fmt.Errorf("%w: store and attributes are required", ErrConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Kind == "" || len(cfg.Features) == 0 {
		return nil, fmt.Errorf("%w: kind and at least one feature are required", ErrConfig)
	}
	if cfg.Relation != (store.Relation{}) && cfg.Relation.Subject != cfg.Kind {
		return nil, fmt.Errorf("%w: relation %q must have subject kind %q", ErrConfig, cfg.Relation, cfg.Kind)
	}
	for i, f := range cfg.Features {
		switch {
		case f.Weight < 0 || math.IsNaN(f.Weight) || math.IsInf(f.Weight, 0):
			return nil, fmt.Errorf("%w: feature %s: weight %v", ErrConfig, f.Name, f.Weight)
		case slices.ContainsFunc(cfg.Features[:i], func(g Feature) bool { return g.Name == f.Name }):
			return nil, fmt.Errorf("%w: feature %s listed twice", ErrConfig, f.Name)
		case f.Name == Taste || f.Name == Appearance:
			if roles == nil {
				return nil, fmt.Errorf("%w: feature %s needs a factor model", ErrConfig, f.Name)
			}
		default:
			spec, ok := attrs.Schema().Spec(cfg.Kind, f.Name)
			if !ok {
				return nil, fmt.Errorf("%w: unknown attribute %s.%s", ErrConfig, cfg.Kind, f.Name)
			}
			if spec.Sensitive && !cfg.AllowSensitive {
				return nil, fmt.Errorf("%w: %s.%s is sensitive; set allow_sensitive to cluster on it", ErrConfig, cfg.Kind, f.Name)
			}
		}
		if f.Weight == 0 {
			cfg.Features[i].Weight = 1
		}
	}
	cfg.Features = slices.Clone(cfg.Features)
	return &Segmenter{store: s, attrs: attrs, roles: roles, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (s *Segmenter) Config() Config { return s.cfg }

// Fit clusters the entities of the kind known to the store that have at
// least one feature, and computes the segment profiles.
func (s *Segmenter) Fit(ctx context.Context) error {
	var refs []recoant.Ref
	for i := range s.store.Len(s.cfg.Kind) {
		if ref, ok := s.store.Ref(s.cfg.Kind, uint32(i)); ok {
			refs = append(refs, ref)
		}
	}
	encs := make([]encoder, len(s.cfg.Features))
	for i, f := range s.cfg.Features {
		encs[i] = s.newEncoder(f, refs)
	}
	f := &fitted{enc: encs, index: map[recoant.Ref]int{}}
	for _, ref := range refs {
		if p, ok := s.encode(encs, ref); ok {
			f.index[ref] = len(f.refs)
			f.refs, f.points = append(f.refs, ref), append(f.points, p)
		}
	}
	c, err := KMeans(ctx, f.points, s.cfg.KMeansConfig)
	if err != nil {
		return err
	}
	f.centers, f.assign = c.Centers, c.Assign
	if f.profiles, err = s.profiles(f); err != nil {
		return err
	}
	s.mu.Lock()
	s.fit = f
	s.mu.Unlock()
	return nil
}

// Refresh fits again; see [Segmenter.Fit].
func (s *Segmenter) Refresh(ctx context.Context) error { return s.Fit(ctx) }

// Segment returns the segment of ref: its cluster from the last Fit, or,
// for an entity that was not part of it, the nearest center of its current
// features. ok is false before Fit and for entities without features.
func (s *Segmenter) Segment(ref recoant.Ref) (segment int, ok bool) {
	s.mu.RLock()
	f := s.fit
	s.mu.RUnlock()
	if f == nil || ref.Kind != s.cfg.Kind {
		return 0, false
	}
	if i, ok := f.index[ref]; ok {
		return f.assign[i], true
	}
	p, ok := s.encode(f.enc, ref)
	if !ok || len(f.centers) == 0 {
		return 0, false
	}
	j, _ := nearest(f.centers, p)
	return j, true
}

// Members returns the entities of a segment from the last Fit.
func (s *Segmenter) Members(segment int) []recoant.Ref {
	s.mu.RLock()
	f := s.fit
	s.mu.RUnlock()
	if f == nil {
		return nil
	}
	var out []recoant.Ref
	for i, a := range f.assign {
		if a == segment {
			out = append(out, f.refs[i])
		}
	}
	return out
}

// Profiles returns the profiles of all segments, by segment number; nil
// before Fit.
func (s *Segmenter) Profiles() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fit == nil {
		return nil
	}
	return slices.Clone(s.fit.profiles)
}

// encoder turns one feature of an entity into numbers.
type encoder struct {
	f      Feature
	typ    attr.Type // empty for Taste and Appearance
	dim    int
	values map[string]int // category and set values → dimension
	mean   []float64      // number and geo
	std    []float64
	scale  float64 // makes the mean squared norm 1, times the weight
}

func (s *Segmenter) newEncoder(f Feature, refs []recoant.Ref) encoder {
	e := encoder{f: f, scale: 1}
	if f.Name != Taste && f.Name != Appearance {
		spec, _ := s.attrs.Schema().Spec(s.cfg.Kind, f.Name)
		e.typ = spec.Type
	}
	switch e.typ {
	case attr.Category, attr.Set:
		e.values = map[string]int{}
		var all []string
		for _, ref := range refs {
			if v, ok := s.attrs.Get(ref, f.Name); ok {
				all = append(all, append(v.Set, v.Category)...)
			}
		}
		slices.Sort(all)
		for _, x := range slices.Compact(all) {
			if x != "" {
				e.values[x] = len(e.values)
			}
		}
		e.dim = len(e.values)
	case "": // Taste or Appearance: the model's number of factors
		for _, ref := range refs {
			if x, ok := e.raw(s, ref); ok {
				e.dim = len(x)
				break
			}
		}
	case attr.Number, attr.Geo:
		e.dim = 1
		if e.typ == attr.Geo {
			e.dim = 2
		}
		var xs [][]float64
		for _, ref := range refs {
			if x, ok := e.raw(s, ref); ok {
				xs = append(xs, x)
			}
		}
		e.mean, e.std = moments(xs, e.dim)
	}
	// Scale the block so that its mean squared norm is 1, then weight it.
	sum, n := 0.0, 0
	for _, ref := range refs {
		if x, ok := e.encode(s, ref); ok {
			sum, n = sum+sqDist(x, make([]float64, len(x))), n+1
		}
	}
	if n > 0 && sum > 0 {
		e.scale = math.Sqrt(float64(n) / sum)
	}
	e.scale *= f.Weight
	return e
}

func moments(xs [][]float64, dim int) (mean, std []float64) {
	mean, std = make([]float64, dim), make([]float64, dim)
	for _, x := range xs {
		add(mean, x, 1/float64(len(xs)))
	}
	for _, x := range xs {
		for d := range x {
			std[d] += (x[d] - mean[d]) * (x[d] - mean[d]) / float64(len(xs))
		}
	}
	for d := range std {
		std[d] = math.Sqrt(std[d])
		if std[d] == 0 {
			std[d] = 1
		}
	}
	return mean, std
}

// raw returns the unscaled numbers of a number, geo, taste or appearance
// feature.
func (e encoder) raw(s *Segmenter, ref recoant.Ref) ([]float64, bool) {
	switch e.f.Name {
	case Taste:
		return s.roles.Taste(ref)
	case Appearance:
		return s.roles.Appearance(ref)
	}
	v, ok := s.attrs.Get(ref, e.f.Name)
	if !ok {
		return nil, false
	}
	if e.typ == attr.Geo {
		return []float64{v.Geo.Lat, v.Geo.Lon}, true
	}
	return []float64{v.Number}, true
}

// encode returns the feature before scaling; ok is false if the entity
// lacks it.
func (e encoder) encode(s *Segmenter, ref recoant.Ref) ([]float64, bool) {
	switch e.typ {
	case attr.Category, attr.Set:
		v, ok := s.attrs.Get(ref, e.f.Name)
		if !ok {
			return nil, false
		}
		x := make([]float64, e.dim)
		for _, val := range append(v.Set, v.Category) {
			if d, ok := e.values[val]; ok {
				x[d] = 1
			}
		}
		return x, true
	case attr.Number, attr.Geo:
		x, ok := e.raw(s, ref)
		if !ok {
			return nil, false
		}
		for d := range x {
			x[d] = (x[d] - e.mean[d]) / e.std[d]
		}
		return x, true
	}
	return e.raw(s, ref)
}

// encode returns the scaled feature vector of an entity: missing blocks are
// zero; ok is false if all are missing.
func (s *Segmenter) encode(encs []encoder, ref recoant.Ref) ([]float64, bool) {
	var p []float64
	found := false
	for _, e := range encs {
		x, ok := e.encode(s, ref)
		if ok && len(x) > 0 {
			found = true
			for d := range x {
				x[d] *= e.scale
			}
		}
		if !ok || len(x) != e.dim {
			x = make([]float64, e.dim)
		}
		p = append(p, x...)
	}
	return p, found
}

// typicalLen is the number of typical objects in a segment profile.
const typicalLen = 10

// profiles computes the segment profiles.
func (s *Segmenter) profiles(f *fitted) ([]Profile, error) {
	ps := make([]Profile, len(f.centers))
	for j := range ps {
		ps[j] = Profile{Segment: j, Attributes: map[string]Summary{}}
	}
	for _, a := range f.assign {
		ps[a].Size++
	}
	members := make([][]recoant.Ref, len(ps))
	for i, a := range f.assign {
		members[a] = append(members[a], f.refs[i])
	}
	for j := range ps {
		ps[j].Share = float64(ps[j].Size) / float64(max(len(f.refs), 1))
		for _, name := range s.profileAttributes() {
			ps[j].Attributes[name] = s.summarize(name, members[j])
		}
	}
	if s.cfg.Relation == (store.Relation{}) {
		return ps, nil
	}
	m, err := s.store.Matrix(s.cfg.Relation, s.store.Stats().Latest)
	if errors.Is(err, store.ErrUnknownRelation) {
		return ps, nil // no signals yet, no activity
	}
	if err != nil {
		return nil, err
	}
	typical := make([]map[uint32]float64, len(ps))
	pairs, negative := make([]int, len(ps)), make([]int, len(ps))
	for i, ref := range f.refs {
		si, _ := s.store.Index(ref)
		if int(si) >= m.Rows {
			continue
		}
		j := f.assign[i]
		if typical[j] == nil {
			typical[j] = map[uint32]float64{}
		}
		cols, vals := m.Row(int(si))
		for n, c := range cols {
			pairs[j]++
			if vals[n] < 0 {
				negative[j]++
				continue
			}
			typical[j][c] += float64(vals[n])
		}
	}
	for j := range ps {
		if ps[j].Size == 0 {
			continue
		}
		ps[j].Activity = float64(pairs[j]) / float64(ps[j].Size)
		if pairs[j] > 0 {
			ps[j].NegativeShare = float64(negative[j]) / float64(pairs[j])
		}
		tk := topk.New(typicalLen)
		for c, v := range typical[j] {
			tk.Push(c, v)
		}
		for _, e := range tk.Sorted() {
			if ref, ok := s.store.Ref(s.cfg.Relation.Object, e.Index); ok {
				ps[j].Typical = append(ps[j].Typical, recoant.Scored{Ref: ref, Score: e.Score})
			}
		}
	}
	return ps, nil
}

// profileAttributes returns the attributes of the kind that profiles
// describe: all but geo, and sensitive ones only if allowed.
func (s *Segmenter) profileAttributes() []string {
	var out []string
	for name, spec := range s.attrs.Schema()[s.cfg.Kind] {
		if spec.Type != attr.Geo && (!spec.Sensitive || s.cfg.AllowSensitive) && !slices.Contains(s.cfg.Ignore, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Profile describes a segment.
type Profile struct {
	Segment int     `json:"segment"`
	Size    int     `json:"size"`
	Share   float64 `json:"share"` // of all segmented entities
	// Activity is the mean number of objects a member interacted with in
	// the configured relation.
	Activity float64 `json:"activity"`
	// NegativeShare is the share of those interactions with a negative
	// preference (passes, returns, bad ratings).
	NegativeShare float64 `json:"negative_share"`
	// Typical are the objects with the highest summed positive preference
	// of the members.
	Typical []recoant.Scored `json:"typical,omitempty"`
	// Attributes summarize the members' attributes.
	Attributes map[string]Summary `json:"attributes,omitempty"`
}

// Summary describes an attribute in a group of entities.
type Summary struct {
	// Coverage is the share of entities with a value.
	Coverage float64 `json:"coverage"`
	// Values are the most frequent category or set values with the share
	// of entities that have them (up to 5).
	Values []ValueShare `json:"values,omitempty"`
	// Mean, Min and Max describe a number attribute.
	Mean float64 `json:"mean,omitempty"`
	Min  float64 `json:"min,omitempty"`
	Max  float64 `json:"max,omitempty"`
}

// ValueShare is a value and the share of entities that have it.
type ValueShare struct {
	Value string  `json:"value"`
	Share float64 `json:"share"`
}

func (s *Segmenter) summarize(name string, refs []recoant.Ref) Summary {
	var sum Summary
	counts := map[string]int{}
	n := 0
	for _, ref := range refs {
		v, ok := s.attrs.Get(ref, name)
		if !ok {
			continue
		}
		n++
		switch v.Type {
		case attr.Number:
			if n == 1 {
				sum.Min, sum.Max = v.Number, v.Number
			}
			sum.Mean += v.Number
			sum.Min, sum.Max = min(sum.Min, v.Number), max(sum.Max, v.Number)
		default:
			for _, x := range append(v.Set, v.Category) {
				if x != "" {
					counts[x]++
				}
			}
		}
	}
	if n == 0 {
		return sum
	}
	sum.Coverage = float64(n) / float64(len(refs))
	sum.Mean /= float64(n)
	for v, c := range counts {
		sum.Values = append(sum.Values, ValueShare{v, float64(c) / float64(n)})
	}
	slices.SortFunc(sum.Values, func(a, b ValueShare) int {
		return cmp.Or(cmp.Compare(b.Share, a.Share), cmp.Compare(a.Value, b.Value))
	})
	sum.Values = sum.Values[:min(5, len(sum.Values))]
	return sum
}

// EntityProfile describes one entity, for assessing users and explaining
// recommendations.
type EntityProfile struct {
	Ref     recoant.Ref `json:"ref"`
	Segment int         `json:"segment"`
	// Distance is the distance to the segment's center relative to the
	// mean distance of the segment's members: below 1 typical, above 1
	// unusual for the segment.
	Distance float64 `json:"distance"`
	// Interactions, Positive and Negative count the objects the entity
	// interacted with in the configured relation, and how many of them it
	// liked or rejected.
	Interactions int `json:"interactions"`
	Positive     int `json:"positive"`
	Negative     int `json:"negative"`
	// Nearest are the most similar segmented entities by their features;
	// the score is 1/(1+distance).
	Nearest []recoant.Scored `json:"nearest,omitempty"`
}

// Profile describes an entity with its n nearest entities; ok is false if
// it has no segment.
func (s *Segmenter) Profile(ref recoant.Ref, n int) (EntityProfile, bool) {
	seg, ok := s.Segment(ref)
	if !ok {
		return EntityProfile{}, false
	}
	s.mu.RLock()
	f := s.fit
	s.mu.RUnlock()
	p, _ := s.encode(f.enc, ref)
	if i, ok := f.index[ref]; ok {
		p = f.points[i]
	}
	prof := EntityProfile{Ref: ref, Segment: seg}
	// Distance relative to the mean distance in the segment.
	mean, count := 0.0, 0
	tk := topk.New(max(n, 1))
	for i, q := range f.points {
		if f.assign[i] == seg {
			mean, count = mean+math.Sqrt(sqDist(q, f.centers[seg])), count+1
		}
		if n > 0 && f.refs[i] != ref {
			tk.Push(uint32(i), 1/(1+math.Sqrt(sqDist(p, q))))
		}
	}
	if d := math.Sqrt(sqDist(p, f.centers[seg])); count > 0 && mean > 0 {
		prof.Distance = d / (mean / float64(count))
	}
	if n > 0 {
		for _, e := range tk.Sorted() {
			prof.Nearest = append(prof.Nearest, recoant.Scored{Ref: f.refs[e.Index], Score: e.Score})
		}
	}
	if s.cfg.Relation != (store.Relation{}) {
		for _, x := range s.store.Interactions(ref, s.cfg.Relation.Object, s.store.Stats().Latest) {
			prof.Interactions++
			switch {
			case x.Score > 0:
				prof.Positive++
			case x.Score < 0:
				prof.Negative++
			}
		}
	}
	return prof, true
}
