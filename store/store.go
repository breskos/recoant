// Package store keeps the relations between subjects and objects, built
// incrementally from a stream of signals.
//
// The [signal.Model] routes every signal to a relation and a channel. A
// relation connects two kinds, e.g. (user, product), and may have a name,
// e.g. "likes", so that several relations can connect the same kinds. Each
// relation has channels — by default "positive", "negative" and "rating" —
// with their own aggregation (sum, last, max, mean) and decay. Thanks to
// forward decay, adding a signal costs O(log n) in the size of the subject's
// row and never rewrites older values.
//
// Algorithms read the preference of a relation: the sum over its channels
// of Weight·(value − Neutral), so positive feedback counts up, negative
// feedback down and ratings around their neutral point. [Store.Matrix]
// returns it as a [sparse.CSR]; [Store.ChannelMatrix] returns the raw values
// of one channel, e.g. the star ratings.
//
// IDs are interned into dense uint32 indexes per kind. A Store is safe for
// concurrent use.
package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/dict"
	"github.com/breskos/recoant/internal/fdecay"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/sparse"
)

var (
	// ErrUnknownRelation is returned for a relation without signals.
	ErrUnknownRelation = errors.New("store: unknown relation")
	// ErrUnknownChannel is returned for a channel a relation does not have.
	ErrUnknownChannel = errors.New("store: unknown channel")
)

// Relation is a directed connection between two kinds, such as
// (user, product). Name distinguishes several relations between the same
// kinds; it is empty for signals whose rule names no relation.
type Relation struct {
	Name    string       `json:"name,omitempty"`
	Subject recoant.Kind `json:"subject"`
	Object  recoant.Kind `json:"object"`
}

// String returns "subject→object", prefixed with "name: " for named
// relations.
func (r Relation) String() string {
	s := string(r.Subject) + "→" + string(r.Object)
	if r.Name != "" {
		return r.Name + ": " + s
	}
	return s
}

// channel holds the values of one channel of a relation.
type channel struct {
	spec   signal.ChannelSpec
	clock  fdecay.Clock
	rows   [][]sparse.Entry // by subject index, sorted by object index
	counts [][]sparse.Entry // number of values, for Mean
	nnz    int
}

func (c *channel) row(i uint32) []sparse.Entry {
	if int(i) < len(c.rows) {
		return c.rows[i]
	}
	return nil
}

// value returns the stored value of entry k of subject i's row at the
// factor f (forward decay), resolving Mean.
func (c *channel) value(i uint32, k int, f float64) float64 {
	e := c.rows[i][k]
	if c.spec.Aggregate == signal.Mean {
		n, _ := sparse.Get(c.counts[i], e.Col)
		return float64(e.Val) / float64(n)
	}
	return float64(e.Val) * f
}

type relation struct {
	channels map[string]*channel
	names    []string // sorted channel names
	// subjSeq and objSeq hold the store's change counter at the last
	// change of each subject's row and object's column.
	subjSeq, objSeq []uint64
}

func setSeq(seqs []uint64, i uint32, seq uint64) []uint64 {
	if int(i) >= len(seqs) {
		seqs = append(seqs, make([]uint64, int(i)+1-len(seqs))...)
	}
	seqs[i] = seq
	return seqs
}

func grow(rows [][]sparse.Entry, i uint32) [][]sparse.Entry {
	if int(i) >= len(rows) {
		rows = append(rows, make([][]sparse.Entry, int(i)+1-len(rows))...)
	}
	return rows
}

// Store holds relations between subjects and objects. Create it with [New].
type Store struct {
	mu      sync.RWMutex
	model   signal.Model
	dicts   map[recoant.Kind]*dict.Dictionary
	rels    map[Relation]*relation
	latest  time.Time
	signals int64
	ignored int64
	seq     uint64
}

// New returns an empty store that routes and weights signals with m.
func New(m signal.Model) (*Store, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &Store{
		model: m,
		dicts: make(map[recoant.Kind]*dict.Dictionary),
		rels:  make(map[Relation]*relation),
	}, nil
}

// Model returns the signal model.
func (s *Store) Model() signal.Model { return s.model }

// RelationOf returns the relation a signal belongs to. ok is false if the
// model ignores the signal's type.
func (s *Store) RelationOf(sig recoant.Signal) (Relation, bool) {
	name, _, _, ok := s.model.Route(sig)
	return Relation{Name: name, Subject: sig.Subject.Kind, Object: sig.Object.Kind}, ok
}

// Add records a signal. Signals of a type the model ignores are counted but
// change nothing. A signal for a declared relation must connect its kinds.
func (s *Store) Add(sig recoant.Signal) error {
	if err := s.check(sig); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(sig)
	return nil
}

func (s *Store) check(sig recoant.Signal) error {
	if err := sig.Validate(); err != nil {
		return err
	}
	name, _, _, ok := s.model.Route(sig)
	if !ok || name == "" {
		return nil
	}
	if k, declared := s.model.Relations[name]; declared && (k.Subject != sig.Subject.Kind || k.Object != sig.Object.Kind) {
		return fmt.Errorf("%w: relation %q connects %s→%s, signal %s→%s", recoant.ErrInvalidSignal, name, k.Subject, k.Object, sig.Subject.Kind, sig.Object.Kind)
	}
	return nil
}

// AddBatch records signals in order. It stops at the first invalid signal
// and returns its error along with the number of signals added before it.
func (s *Store) AddBatch(ctx context.Context, sigs []recoant.Signal) (int, error) {
	const chunk = 4096
	for start := 0; start < len(sigs); start += chunk {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		end := min(start+chunk, len(sigs))
		for i := start; i < end; i++ {
			if err := s.check(sigs[i]); err != nil {
				return i, fmt.Errorf("signal %d: %w", i, err)
			}
		}
		s.mu.Lock()
		for _, sig := range sigs[start:end] {
			s.add(sig)
		}
		s.mu.Unlock()
	}
	return len(sigs), nil
}

func (s *Store) add(sig recoant.Signal) {
	s.signals++
	if sig.Time.After(s.latest) {
		s.latest = sig.Time
	}
	name, chName, v, ok := s.model.Route(sig)
	r, _ := s.model.RuleFor(sig.Type)
	if !ok || (v == 0 && r.FeedbackKind() != signal.Rating) {
		s.ignored++
		return
	}
	key := Relation{Name: name, Subject: sig.Subject.Kind, Object: sig.Object.Kind}
	rel := s.rels[key]
	if rel == nil {
		rel = &relation{channels: make(map[string]*channel)}
		s.rels[key] = rel
	}
	ch := rel.channels[chName]
	if ch == nil {
		spec, _ := s.model.ChannelSpec(name, chName)
		ch = &channel{spec: spec, clock: fdecay.Clock{Rate: spec.Rate}}
		rel.channels[chName] = ch
		rel.names = append(rel.names, chName)
		slices.Sort(rel.names)
	}
	if ch.clock.NeedsRebase(sig.Time) {
		ch.rebase(sig.Time)
	}
	si := s.dict(sig.Subject.Kind).Intern(sig.Subject.ID)
	oi := s.dict(sig.Object.Kind).Intern(sig.Object.ID)
	stored := float32(v * ch.clock.Scale(sig.Time))
	ch.rows = grow(ch.rows, si)
	before := len(ch.rows[si])
	switch ch.spec.Aggregate {
	case signal.Last:
		ch.rows[si] = sparse.Set(ch.rows[si], oi, stored)
	case signal.Max:
		ch.rows[si] = sparse.Max(ch.rows[si], oi, stored)
	case signal.Mean:
		ch.rows[si] = sparse.Add(ch.rows[si], oi, stored)
		ch.counts = grow(ch.counts, si)
		ch.counts[si] = sparse.Add(ch.counts[si], oi, 1)
	default:
		ch.rows[si] = sparse.Add(ch.rows[si], oi, stored)
	}
	ch.nnz += len(ch.rows[si]) - before
	s.seq++
	rel.subjSeq = setSeq(rel.subjSeq, si, s.seq)
	rel.objSeq = setSeq(rel.objSeq, oi, s.seq)
}

func (c *channel) rebase(t time.Time) {
	f := float32(c.clock.Rebase(t))
	for _, row := range c.rows {
		for k := range row {
			row[k].Val *= f
		}
	}
}

func (s *Store) dict(k recoant.Kind) *dict.Dictionary {
	d := s.dicts[k]
	if d == nil {
		d = dict.New()
		s.dicts[k] = d
	}
	return d
}

// Seq returns the store's change counter. It grows with every signal that
// changes a relation; pass it to [Store.Changes] later to find what changed
// since. The counter starts at 0 for a new or restored store.
func (s *Store) Seq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// Changes returns the indexes of the subjects whose rows and the objects
// whose columns changed in a relation after the change counter was since.
// Algorithms use it to update incrementally.
func (s *Store) Changes(rel Relation, since uint64) (subjects, objects []uint32) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.rels[rel]
	if r == nil {
		return nil, nil
	}
	changed := func(seqs []uint64) []uint32 {
		var out []uint32
		for i, q := range seqs {
			if q > since {
				out = append(out, uint32(i))
			}
		}
		return out
	}
	return changed(r.subjSeq), changed(r.objSeq)
}

// prefRow returns the preference row of subject i in r at time now.
func (s *Store) prefRow(r *relation, i uint32, now time.Time) []sparse.Entry {
	if len(r.names) == 1 {
		ch := r.channels[r.names[0]]
		row := ch.row(i)
		out := make([]sparse.Entry, len(row))
		f := ch.clock.At(now)
		for k, e := range row {
			out[k] = sparse.Entry{Col: e.Col, Val: float32(ch.spec.Weight * (ch.value(i, k, f) - ch.spec.Neutral))}
		}
		return out
	}
	var out []sparse.Entry
	for _, name := range r.names {
		ch := r.channels[name]
		f := ch.clock.At(now)
		for k, e := range ch.row(i) {
			out = append(out, sparse.Entry{Col: e.Col, Val: float32(ch.spec.Weight * (ch.value(i, k, f) - ch.spec.Neutral))})
		}
	}
	slices.SortStableFunc(out, func(a, b sparse.Entry) int { return cmp.Compare(a.Col, b.Col) })
	merged := out[:0]
	for _, e := range out {
		if n := len(merged); n > 0 && merged[n-1].Col == e.Col {
			merged[n-1].Val += e.Val
			continue
		}
		merged = append(merged, e)
	}
	return merged
}

func (s *Store) subjectRows(r *relation) int {
	n := 0
	for _, ch := range r.channels {
		n = max(n, len(ch.rows))
	}
	return n
}

// Row returns the preference row of the subject with dense index subject
// in a relation at time now (zero: the latest signal). It is empty for
// unknown relations and subjects.
func (s *Store) Row(rel Relation, subject uint32, now time.Time) []sparse.Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.rels[rel]
	if r == nil {
		return nil
	}
	if now.IsZero() {
		now = s.latest
	}
	row := s.prefRow(r, subject, now)
	if len(row) == 0 {
		return nil
	}
	return row
}

// Matrix returns a copy of a relation's preference as a matrix at time now
// (zero: the latest signal). Rows are subject indexes, columns object
// indexes (see [Store.Index] and [Store.Ref]). Values are negative where
// negative feedback outweighs positive feedback.
func (s *Store) Matrix(rel Relation, now time.Time) (*sparse.CSR, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.rels[rel]
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRelation, rel)
	}
	if now.IsZero() {
		now = s.latest
	}
	n := s.subjectRows(r)
	rows := make([][]sparse.Entry, n)
	for i := range n {
		rows[i] = s.prefRow(r, uint32(i), now)
	}
	return sparse.FromRows(rows, s.dicts[rel.Subject].Len(), s.dicts[rel.Object].Len(), 1), nil
}

// ChannelMatrix returns the raw values of one channel of a relation at time
// now (zero: the latest signal), e.g. the star ratings of a rating channel.
func (s *Store) ChannelMatrix(rel Relation, name string, now time.Time) (*sparse.CSR, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.rels[rel]
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRelation, rel)
	}
	ch := r.channels[name]
	if ch == nil {
		return nil, fmt.Errorf("%w: %q in %s", ErrUnknownChannel, name, rel)
	}
	if now.IsZero() {
		now = s.latest
	}
	f := ch.clock.At(now)
	rows := make([][]sparse.Entry, len(ch.rows))
	for i, row := range ch.rows {
		rows[i] = make([]sparse.Entry, len(row))
		for k, e := range row {
			rows[i][k] = sparse.Entry{Col: e.Col, Val: float32(ch.value(uint32(i), k, f))}
		}
	}
	return sparse.FromRows(rows, s.dicts[rel.Subject].Len(), s.dicts[rel.Object].Len(), 1), nil
}

// Channels returns the channels of a relation, sorted.
func (s *Store) Channels(rel Relation) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r := s.rels[rel]; r != nil {
		return slices.Clone(r.names)
	}
	return nil
}

// pairValue returns the preference of a pair summed over all relations
// between their kinds, and whether any relation has an entry.
func (s *Store) pairValue(subject, object recoant.Ref, now time.Time) (float64, bool) {
	si, ok := s.index(subject)
	if !ok {
		return 0, false
	}
	oi, ok := s.index(object)
	if !ok {
		return 0, false
	}
	var sum float64
	found := false
	for key, r := range s.rels {
		if key.Subject != subject.Kind || key.Object != object.Kind {
			continue
		}
		for _, name := range r.names {
			ch := r.channels[name]
			row := ch.row(si)
			k, ok := slices.BinarySearchFunc(row, oi, func(e sparse.Entry, c uint32) int { return cmp.Compare(e.Col, c) })
			if !ok {
				continue
			}
			found = true
			sum += ch.spec.Weight * (ch.value(si, k, ch.clock.At(now)) - ch.spec.Neutral)
		}
	}
	return sum, found
}

// Has reports whether the subject has any recorded interaction with the
// object, positive or negative, in any relation between their kinds. It
// implements [recoant.Seen].
func (s *Store) Has(subject, object recoant.Ref) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, found := s.pairValue(subject, object, s.latest)
	return found
}

// Rejected reports whether the subject's preference for the object, summed
// over all relations between their kinds, is negative: negative feedback
// or a rating below the neutral point outweighs the rest.
func (s *Store) Rejected(subject, object recoant.Ref) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, _ := s.pairValue(subject, object, s.latest)
	return v < 0
}

// Strength returns the preference of the pair at time now, summed over all
// relations between their kinds, or 0.
func (s *Store) Strength(subject, object recoant.Ref, now time.Time) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, _ := s.pairValue(subject, object, now)
	return v
}

// Interactions returns the objects of the given kind the subject interacted
// with and their preference at time now, summed over all relations between
// the kinds, strongest first.
func (s *Store) Interactions(subject recoant.Ref, object recoant.Kind, now time.Time) []recoant.Scored {
	s.mu.RLock()
	defer s.mu.RUnlock()
	si, ok := s.index(subject)
	if !ok {
		return nil
	}
	sums := make(map[uint32]float64)
	for key, r := range s.rels {
		if key.Subject == subject.Kind && key.Object == object {
			for _, e := range s.prefRow(r, si, now) {
				sums[e.Col] += float64(e.Val)
			}
		}
	}
	if len(sums) == 0 {
		return nil
	}
	d := s.dicts[object]
	out := make([]recoant.Scored, 0, len(sums))
	for j, v := range sums {
		out = append(out, recoant.Scored{Ref: recoant.Ref{Kind: object, ID: d.ID(j)}, Score: v})
	}
	slices.SortFunc(out, func(a, b recoant.Scored) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(a.Ref.ID, b.Ref.ID)
	})
	return out
}

// Index returns the dense index of a reference.
func (s *Store) Index(ref recoant.Ref) (uint32, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index(ref)
}

func (s *Store) index(ref recoant.Ref) (uint32, bool) {
	d := s.dicts[ref.Kind]
	if d == nil {
		return 0, false
	}
	return d.Lookup(ref.ID)
}

// Ref returns the reference at a dense index of a kind.
func (s *Store) Ref(kind recoant.Kind, i uint32) (recoant.Ref, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.dicts[kind]
	if d == nil || int(i) >= d.Len() {
		return recoant.Ref{}, false
	}
	return recoant.Ref{Kind: kind, ID: d.ID(i)}, true
}

// Len returns the number of known references of a kind.
func (s *Store) Len(kind recoant.Kind) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if d := s.dicts[kind]; d != nil {
		return d.Len()
	}
	return 0
}

// Kinds returns the known kinds, sorted.
func (s *Store) Kinds() []recoant.Kind {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks := make([]recoant.Kind, 0, len(s.dicts))
	for k := range s.dicts {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// Relations returns the relations with signals, sorted.
func (s *Store) Relations() []Relation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rs := make([]Relation, 0, len(s.rels))
	for r := range s.rels {
		rs = append(rs, r)
	}
	slices.SortFunc(rs, compareRelations)
	return rs
}

func compareRelations(a, b Relation) int {
	if c := cmp.Compare(a.Subject, b.Subject); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Object, b.Object); c != 0 {
		return c
	}
	return cmp.Compare(a.Name, b.Name)
}

// Stats describes the content of a store.
type Stats struct {
	Signals      int64     // signals added
	Ignored      int64     // signals the model gave no weight
	Interactions int       // distinct (relation, subject, object) entries
	Latest       time.Time // time of the latest signal
}

// Stats returns counts of the store's content.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Signals: s.signals, Ignored: s.ignored, Latest: s.latest}
	for _, r := range s.rels {
		if len(r.names) == 1 {
			st.Interactions += r.channels[r.names[0]].nnz
			continue
		}
		for i := range s.subjectRows(r) {
			st.Interactions += len(s.prefRow(r, uint32(i), s.latest))
		}
	}
	return st
}
