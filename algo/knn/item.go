package knn

import (
	"context"
	"sync"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

// ItemKNN recommends objects similar to those a subject interacted with
// (subject→object) and objects similar to an object (object→object).
// It is safe for concurrent use.
type ItemKNN struct {
	store *store.Store
	cfg   Config

	mu        sync.RWMutex
	neighbors [][]Neighbor // by object index
	seq       uint64       // store change counter at the last Fit or Refresh
	fitted    bool
}

// NewItemKNN returns an untrained model that learns from s.
func NewItemKNN(s *store.Store, cfg Config) (*ItemKNN, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &ItemKNN{store: s, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (m *ItemKNN) Config() Config { return m.cfg }

// objectMatrices returns the object×subject matrix and its transpose.
func objectMatrices(s *store.Store, cfg Config) (a, at *sparse.CSR, seq uint64, err error) {
	seq = s.Seq()
	r, err := s.Matrix(cfg.Relation, recoantZero)
	if err != nil {
		return nil, nil, 0, err
	}
	if cfg.Similarity == AdjustedCosine {
		center(r)
	}
	return r.Transpose(), r, seq, nil
}

// Fit computes the neighbors of all objects.
func (m *ItemKNN) Fit(ctx context.Context) error {
	a, at, seq, err := objectMatrices(m.store, m.cfg)
	if err != nil {
		return err
	}
	table, err := fitTable(ctx, a, at, m.cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.neighbors, m.seq, m.fitted = table, seq, true
	return nil
}

// Refresh updates the neighbors of the objects that received new
// interactions since the last Fit or Refresh, and their entries in the
// lists of other objects. It fits the model if it was not fitted yet.
//
// The refreshed objects' own neighbors are exact. Other lists may keep a
// slightly outdated entry or miss a candidate that was pruned earlier; a
// periodic Fit restores exactness.
func (m *ItemKNN) Refresh(ctx context.Context) error {
	m.mu.RLock()
	fitted, since := m.fitted, m.seq
	m.mu.RUnlock()
	if !fitted {
		return m.Fit(ctx)
	}
	_, changed := m.store.Changes(m.cfg.Relation, since)
	if len(changed) == 0 {
		return nil
	}
	a, at, seq, err := objectMatrices(m.store, m.cfg)
	if err != nil {
		return err
	}
	full, err := fullSims(ctx, a, at, changed, m.cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	table := applySims(m.neighbors, a.Rows, full, m.cfg)
	m.neighbors, m.seq = table, seq
	return nil
}

// Neighbors returns the most similar objects of an object.
func (m *ItemKNN) Neighbors(object recoant.Ref) []recoant.Scored {
	if object.Kind != m.cfg.Relation.Object {
		return nil
	}
	i, ok := m.store.Index(object)
	if !ok {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if int(i) >= len(m.neighbors) {
		return nil
	}
	return toScored(m.store, object.Kind, m.neighbors[i])
}

// Recommend answers subject→object requests (based on the subject's
// history) and object→object requests (similar objects).
func (m *ItemKNN) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rel := m.cfg.Relation
	if req.Target != rel.Object || (req.Subject.Kind != rel.Subject && req.Subject.Kind != rel.Object) {
		return nil, ErrUnsupported
	}
	si, ok := m.store.Index(req.Subject)
	if !ok {
		return nil, nil
	}
	scores := make(map[uint32]float64)
	var history []sparse.Entry
	m.mu.RLock()
	if req.Subject.Kind == rel.Object {
		if int(si) < len(m.neighbors) {
			for _, n := range m.neighbors[si] {
				scores[n.Index] = float64(n.Sim)
			}
		}
	} else {
		history = m.store.Row(rel, si, req.Time)
		for k, e := range history {
			if k%1024 == 0 && ctx.Err() != nil {
				m.mu.RUnlock()
				return nil, ctx.Err()
			}
			if int(e.Col) >= len(m.neighbors) {
				continue
			}
			for _, n := range m.neighbors[e.Col] {
				scores[n.Index] += float64(e.Val) * float64(n.Sim)
			}
		}
	}
	m.mu.RUnlock()
	return rank(m.store, req, scores, history), nil
}
