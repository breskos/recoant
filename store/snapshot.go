package store

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/dict"
	"github.com/breskos/recoant/internal/fdecay"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/sparse"
)

// SnapshotVersion is the version of the [Snapshot] layout.
const SnapshotVersion = 1

// ErrInvalidSnapshot is returned by [FromSnapshot] for inconsistent data.
var ErrInvalidSnapshot = errors.New("store: invalid snapshot")

// Snapshot is the serializable content of a store.
type Snapshot struct {
	Version   int                `json:"version"`
	Model     signal.Model       `json:"model"`
	Epoch     time.Time          `json:"epoch"`
	Latest    time.Time          `json:"latest"`
	Signals   int64              `json:"signals"`
	Ignored   int64              `json:"ignored"`
	Kinds     []KindSnapshot     `json:"kinds"`
	Relations []RelationSnapshot `json:"relations"`
}

// KindSnapshot holds the IDs of a kind in index order.
type KindSnapshot struct {
	Kind recoant.Kind `json:"kind"`
	IDs  []string     `json:"ids"`
}

// RelationSnapshot holds a relation in CSR form with forward-decayed values
// relative to the snapshot's epoch.
type RelationSnapshot struct {
	Relation Relation  `json:"relation"`
	Indptr   []int     `json:"indptr"`
	Indices  []uint32  `json:"indices"`
	Values   []float32 `json:"values"`
}

// Snapshot returns a copy of the store's content.
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sn := &Snapshot{
		Version: SnapshotVersion,
		Model:   s.model,
		Epoch:   s.clock.Epoch,
		Latest:  s.latest,
		Signals: s.signals,
		Ignored: s.ignored,
	}
	for k, d := range s.dicts {
		sn.Kinds = append(sn.Kinds, KindSnapshot{Kind: k, IDs: slices.Clone(d.IDs())})
	}
	slices.SortFunc(sn.Kinds, func(a, b KindSnapshot) int { return cmp.Compare(a.Kind, b.Kind) })
	for r, rel := range s.rels {
		m := sparse.FromRows(rel.rows, len(rel.rows), s.dicts[r.Object].Len(), 1)
		sn.Relations = append(sn.Relations, RelationSnapshot{Relation: r, Indptr: m.Indptr, Indices: m.Indices, Values: m.Values})
	}
	slices.SortFunc(sn.Relations, func(a, b RelationSnapshot) int { return compareRelations(a.Relation, b.Relation) })
	return sn
}

// FromSnapshot restores a store. The snapshot must not be modified
// afterwards.
func FromSnapshot(sn *Snapshot) (*Store, error) {
	if sn == nil {
		return nil, fmt.Errorf("%w: nil", ErrInvalidSnapshot)
	}
	if sn.Version != SnapshotVersion {
		return nil, fmt.Errorf("%w: version %d, want %d", ErrInvalidSnapshot, sn.Version, SnapshotVersion)
	}
	s, err := New(sn.Model)
	if err != nil {
		return nil, err
	}
	s.clock = fdecay.Clock{Rate: sn.Model.Rate(), Epoch: sn.Epoch}
	s.latest, s.signals, s.ignored = sn.Latest, sn.Signals, sn.Ignored
	for _, k := range sn.Kinds {
		if _, dup := s.dicts[k.Kind]; dup || k.Kind == "" {
			return nil, fmt.Errorf("%w: kind %q empty or duplicate", ErrInvalidSnapshot, k.Kind)
		}
		d, ok := dict.FromIDs(k.IDs)
		if !ok {
			return nil, fmt.Errorf("%w: duplicate id in kind %q", ErrInvalidSnapshot, k.Kind)
		}
		s.dicts[k.Kind] = d
	}
	for _, rs := range sn.Relations {
		if err := s.restoreRelation(rs); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) restoreRelation(rs RelationSnapshot) error {
	r := rs.Relation
	if _, dup := s.rels[r]; dup {
		return fmt.Errorf("%w: duplicate relation %s", ErrInvalidSnapshot, r)
	}
	sd, od := s.dicts[r.Subject], s.dicts[r.Object]
	if sd == nil || od == nil {
		return fmt.Errorf("%w: relation %s refers to unknown kind", ErrInvalidSnapshot, r)
	}
	rows := len(rs.Indptr) - 1
	if rows < 0 || rows > sd.Len() || len(rs.Indices) != len(rs.Values) ||
		rs.Indptr[0] != 0 || rs.Indptr[rows] != len(rs.Indices) {
		return fmt.Errorf("%w: relation %s has inconsistent shape", ErrInvalidSnapshot, r)
	}
	rel := &relation{rows: make([][]sparse.Entry, rows), nnz: len(rs.Indices)}
	for i := range rows {
		a, b := rs.Indptr[i], rs.Indptr[i+1]
		if a > b || b > len(rs.Indices) {
			return fmt.Errorf("%w: relation %s row %d has invalid bounds", ErrInvalidSnapshot, r, i)
		}
		row := make([]sparse.Entry, b-a)
		for k := range row {
			col := rs.Indices[a+k]
			if int(col) >= od.Len() || (k > 0 && col <= row[k-1].Col) {
				return fmt.Errorf("%w: relation %s row %d has invalid columns", ErrInvalidSnapshot, r, i)
			}
			row[k] = sparse.Entry{Col: col, Val: rs.Values[a+k]}
		}
		rel.rows[i] = row
	}
	s.rels[r] = rel
	return nil
}
