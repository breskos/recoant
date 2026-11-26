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

// SnapshotVersion is the version of the [Snapshot] layout. Version 1
// (single channel, before v0.4) can still be restored.
const SnapshotVersion = 2

// ErrInvalidSnapshot is returned by [FromSnapshot] for inconsistent data.
var ErrInvalidSnapshot = errors.New("store: invalid snapshot")

// Snapshot is the serializable content of a store.
type Snapshot struct {
	Version   int                `json:"version"`
	Model     signal.Model       `json:"model"`
	Epoch     time.Time          `json:"epoch,omitzero"` // version 1 only
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

// RelationSnapshot holds the channels of a relation.
type RelationSnapshot struct {
	Relation Relation          `json:"relation"`
	Channels []ChannelSnapshot `json:"channels,omitempty"`
	// Indptr, Indices and Values hold the single channel of version 1.
	Indptr  []int     `json:"indptr,omitempty"`
	Indices []uint32  `json:"indices,omitempty"`
	Values  []float32 `json:"values,omitempty"`
}

// ChannelSnapshot holds a channel in CSR form with forward-decayed values
// relative to its epoch.
type ChannelSnapshot struct {
	Name    string    `json:"name"`
	Epoch   time.Time `json:"epoch,omitzero"`
	Indptr  []int     `json:"indptr"`
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
	Counts  []float32 `json:"counts,omitempty"` // for mean channels
}

// Snapshot returns a copy of the store's content.
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sn := &Snapshot{
		Version: SnapshotVersion,
		Model:   s.model,
		Latest:  s.latest,
		Signals: s.signals,
		Ignored: s.ignored,
	}
	for k, d := range s.dicts {
		sn.Kinds = append(sn.Kinds, KindSnapshot{Kind: k, IDs: slices.Clone(d.IDs())})
	}
	slices.SortFunc(sn.Kinds, func(a, b KindSnapshot) int { return cmp.Compare(a.Kind, b.Kind) })
	for key, rel := range s.rels {
		rs := RelationSnapshot{Relation: key}
		for _, name := range rel.names {
			ch := rel.channels[name]
			m := sparse.FromRows(ch.rows, len(ch.rows), s.dicts[key.Object].Len(), 1)
			cs := ChannelSnapshot{Name: name, Epoch: ch.clock.Epoch, Indptr: m.Indptr, Indices: m.Indices, Values: m.Values}
			if ch.spec.Aggregate == signal.Mean {
				cs.Counts = sparse.FromRows(ch.counts, len(ch.rows), s.dicts[key.Object].Len(), 1).Values
			}
			rs.Channels = append(rs.Channels, cs)
		}
		sn.Relations = append(sn.Relations, rs)
	}
	slices.SortFunc(sn.Relations, func(a, b RelationSnapshot) int { return compareRelations(a.Relation, b.Relation) })
	return sn
}

// FromSnapshot restores a store from a snapshot of version 1 or 2. The
// snapshot must not be modified afterwards.
func FromSnapshot(sn *Snapshot) (*Store, error) {
	if sn == nil {
		return nil, fmt.Errorf("%w: nil", ErrInvalidSnapshot)
	}
	if sn.Version != 1 && sn.Version != SnapshotVersion {
		return nil, fmt.Errorf("%w: version %d, want 1 or %d", ErrInvalidSnapshot, sn.Version, SnapshotVersion)
	}
	s, err := New(sn.Model)
	if err != nil {
		return nil, err
	}
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
		if sn.Version == 1 {
			// Before v0.4 everything was one positive channel.
			rs.Channels = []ChannelSnapshot{{Name: "positive", Epoch: sn.Epoch, Indptr: rs.Indptr, Indices: rs.Indices, Values: rs.Values}}
		}
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
	rel := &relation{channels: make(map[string]*channel)}
	for _, cs := range rs.Channels {
		spec, _ := s.model.ChannelSpec(r.Name, cs.Name)
		if _, dup := rel.channels[cs.Name]; dup {
			return fmt.Errorf("%w: relation %s has channel %q twice", ErrInvalidSnapshot, r, cs.Name)
		}
		rows, err := restoreRows(cs.Indptr, cs.Indices, cs.Values, sd.Len(), od.Len())
		if err != nil {
			return fmt.Errorf("%w: relation %s channel %q: %w", ErrInvalidSnapshot, r, cs.Name, err)
		}
		ch := &channel{spec: spec, clock: fdecay.Clock{Rate: spec.Rate, Epoch: cs.Epoch}, rows: rows}
		if spec.Aggregate == signal.Mean {
			if ch.counts, err = restoreRows(cs.Indptr, cs.Indices, cs.Counts, sd.Len(), od.Len()); err != nil {
				return fmt.Errorf("%w: relation %s channel %q counts: %w", ErrInvalidSnapshot, r, cs.Name, err)
			}
		}
		for _, row := range rows {
			ch.nnz += len(row)
		}
		rel.channels[cs.Name] = ch
		rel.names = append(rel.names, cs.Name)
	}
	slices.Sort(rel.names)
	s.rels[r] = rel
	return nil
}

func restoreRows(indptr []int, indices []uint32, values []float32, subjects, objects int) ([][]sparse.Entry, error) {
	rows := len(indptr) - 1
	if rows < 0 || rows > subjects || len(indices) != len(values) || indptr[0] != 0 || indptr[rows] != len(indices) {
		return nil, errors.New("inconsistent shape")
	}
	out := make([][]sparse.Entry, rows)
	for i := range rows {
		a, b := indptr[i], indptr[i+1]
		if a > b || b > len(indices) {
			return nil, fmt.Errorf("row %d has invalid bounds", i)
		}
		row := make([]sparse.Entry, b-a)
		for k := range row {
			col := indices[a+k]
			if int(col) >= objects || (k > 0 && col <= row[k-1].Col) {
				return nil, fmt.Errorf("row %d has invalid columns", i)
			}
			row[k] = sparse.Entry{Col: col, Val: values[a+k]}
		}
		out[i] = row
	}
	return out, nil
}
