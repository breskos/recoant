// Package store keeps the interaction strength between subjects and objects,
// built incrementally from a stream of signals.
//
// Each signal is weighted by a [signal.Model] and added to the pair
// (subject, object) of its relation, for example (user, product). Strengths
// decay over time with the model's half-life. Thanks to forward decay, adding
// a signal costs O(log n) in the size of the subject's row and never rewrites
// older values.
//
// IDs are interned into dense uint32 indexes per kind; rows hold
// (index, float32) pairs. Algorithms read a consistent snapshot of a relation
// as a [sparse.CSR] with [Store.Matrix].
//
// A Store is safe for concurrent use.
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

// ErrUnknownRelation is returned for a relation without signals.
var ErrUnknownRelation = errors.New("store: unknown relation")

// Relation is a pair of kinds signals connect, such as (user, product).
type Relation struct {
	Subject recoant.Kind `json:"subject"`
	Object  recoant.Kind `json:"object"`
}

// String returns "subject→object".
func (r Relation) String() string { return string(r.Subject) + "→" + string(r.Object) }

type relation struct {
	rows [][]sparse.Entry // indexed by subject index, sorted by object index
	nnz  int
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

// Store holds interaction strengths. Create it with [New].
type Store struct {
	mu      sync.RWMutex
	model   signal.Model
	clock   fdecay.Clock
	dicts   map[recoant.Kind]*dict.Dictionary
	rels    map[Relation]*relation
	latest  time.Time
	signals int64
	ignored int64
	seq     uint64
}

// New returns an empty store that weights signals with m.
func New(m signal.Model) (*Store, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &Store{
		model: m,
		clock: fdecay.Clock{Rate: m.Rate()},
		dicts: make(map[recoant.Kind]*dict.Dictionary),
		rels:  make(map[Relation]*relation),
	}, nil
}

// Model returns the signal model.
func (s *Store) Model() signal.Model { return s.model }

// Add records a signal. Signals of a type the model ignores are counted but
// change nothing.
func (s *Store) Add(sig recoant.Signal) error {
	if err := sig.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(sig)
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
			if err := sigs[i].Validate(); err != nil {
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
	w, ok := s.model.Weight(sig)
	if !ok || w == 0 {
		s.ignored++
		return
	}
	if s.clock.NeedsRebase(sig.Time) {
		s.rebase(sig.Time)
	}
	delta := float32(w * s.clock.Scale(sig.Time))
	si := s.dict(sig.Subject.Kind).Intern(sig.Subject.ID)
	oi := s.dict(sig.Object.Kind).Intern(sig.Object.ID)
	key := Relation{Subject: sig.Subject.Kind, Object: sig.Object.Kind}
	rel := s.rels[key]
	if rel == nil {
		rel = &relation{}
		s.rels[key] = rel
	}
	if int(si) >= len(rel.rows) {
		rel.rows = append(rel.rows, make([][]sparse.Entry, int(si)+1-len(rel.rows))...)
	}
	before := len(rel.rows[si])
	rel.rows[si] = sparse.Add(rel.rows[si], oi, delta)
	rel.nnz += len(rel.rows[si]) - before
	s.seq++
	rel.subjSeq = setSeq(rel.subjSeq, si, s.seq)
	rel.objSeq = setSeq(rel.objSeq, oi, s.seq)
}

// Seq returns the store's change counter. It grows with every signal that
// changes an interaction; pass it to [Store.Changes] later to find what
// changed since. The counter starts at 0 for a new or restored store.
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

// Row returns a copy of the row of the subject with dense index subject in
// a relation, with strengths at time now (zero: the latest signal). It is
// empty for unknown relations and subjects.
func (s *Store) Row(rel Relation, subject uint32, now time.Time) []sparse.Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.rels[rel]
	if r == nil || int(subject) >= len(r.rows) {
		return nil
	}
	if now.IsZero() {
		now = s.latest
	}
	f := float32(s.clock.At(now))
	row := make([]sparse.Entry, len(r.rows[subject]))
	for k, e := range r.rows[subject] {
		row[k] = sparse.Entry{Col: e.Col, Val: e.Val * f}
	}
	return row
}

func (s *Store) rebase(t time.Time) {
	f := float32(s.clock.Rebase(t))
	for _, rel := range s.rels {
		for _, row := range rel.rows {
			for k := range row {
				row[k].Val *= f
			}
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

// Has reports whether the subject has a recorded interaction with the
// object. It implements [recoant.Seen].
func (s *Store) Has(subject, object recoant.Ref) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, oi, ok := s.lookup(subject, object)
	if !ok {
		return false
	}
	_, found := sparse.Get(row, oi)
	return found
}

// Strength returns the interaction strength of the pair at time now, or 0.
func (s *Store) Strength(subject, object recoant.Ref, now time.Time) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, oi, ok := s.lookup(subject, object)
	if !ok {
		return 0
	}
	v, _ := sparse.Get(row, oi)
	return float64(v) * s.clock.At(now)
}

func (s *Store) lookup(subject, object recoant.Ref) ([]sparse.Entry, uint32, bool) {
	rel := s.rels[Relation{Subject: subject.Kind, Object: object.Kind}]
	if rel == nil {
		return nil, 0, false
	}
	si, ok := s.index(subject)
	if !ok || int(si) >= len(rel.rows) {
		return nil, 0, false
	}
	oi, ok := s.index(object)
	if !ok {
		return nil, 0, false
	}
	return rel.rows[si], oi, true
}

// Interactions returns the objects of the given kind the subject interacted
// with and their strength at time now, strongest first.
func (s *Store) Interactions(subject recoant.Ref, object recoant.Kind, now time.Time) []recoant.Scored {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rel := s.rels[Relation{Subject: subject.Kind, Object: object}]
	si, ok := s.index(subject)
	if rel == nil || !ok || int(si) >= len(rel.rows) {
		return nil
	}
	f := s.clock.At(now)
	d := s.dicts[object]
	row := rel.rows[si]
	out := make([]recoant.Scored, len(row))
	for k, e := range row {
		out[k] = recoant.Scored{Ref: recoant.Ref{Kind: object, ID: d.ID(e.Col)}, Score: float64(e.Val) * f}
	}
	slices.SortStableFunc(out, func(a, b recoant.Scored) int { return cmp.Compare(b.Score, a.Score) })
	return out
}

// Matrix returns a copy of a relation as a matrix of strengths at time now.
// Rows are subject indexes, columns object indexes (see [Store.Index] and
// [Store.Ref]). If now is zero, the time of the latest signal is used.
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
	return sparse.FromRows(r.rows, s.dicts[rel.Subject].Len(), s.dicts[rel.Object].Len(), float32(s.clock.At(now))), nil
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
	return cmp.Compare(a.Object, b.Object)
}

// Stats describes the content of a store.
type Stats struct {
	Signals      int64     // signals added
	Ignored      int64     // signals the model gave no weight
	Interactions int       // distinct (subject, object) pairs
	Latest       time.Time // time of the latest signal
}

// Stats returns counts of the store's content.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Signals: s.signals, Ignored: s.ignored, Latest: s.latest}
	for _, r := range s.rels {
		st.Interactions += r.nnz
	}
	return st
}
