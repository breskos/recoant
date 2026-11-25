package knn

import (
	"context"
	"sync"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

// UserKNN recommends what similar subjects interacted with
// (subject→object) and similar subjects themselves (subject→subject, e.g.
// user-to-user). It is safe for concurrent use.
type UserKNN struct {
	store *store.Store
	cfg   Config

	mu        sync.RWMutex
	neighbors [][]Neighbor // by subject index
	matrix    *sparse.CSR  // subject×object strengths at the last Fit or Refresh
	seq       uint64
	fitted    bool
}

// NewUserKNN returns an untrained model that learns from s.
func NewUserKNN(s *store.Store, cfg Config) (*UserKNN, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &UserKNN{store: s, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (m *UserKNN) Config() Config { return m.cfg }

// subjectMatrices returns the subject×object matrix used for similarities,
// its transpose and the uncentered matrix used for scoring.
func subjectMatrices(s *store.Store, cfg Config) (a, at, raw *sparse.CSR, seq uint64, err error) {
	seq = s.Seq()
	raw, err = s.Matrix(cfg.Relation, recoantZero)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	a = raw
	if cfg.Similarity == AdjustedCosine {
		a = &sparse.CSR{Rows: raw.Rows, Cols: raw.Cols, Indptr: raw.Indptr, Indices: raw.Indices, Values: append([]float32(nil), raw.Values...)}
		center(a)
	}
	return a, a.Transpose(), raw, seq, nil
}

// Fit computes the neighbors of all subjects.
func (m *UserKNN) Fit(ctx context.Context) error {
	a, at, raw, seq, err := subjectMatrices(m.store, m.cfg)
	if err != nil {
		return err
	}
	table, err := fitTable(ctx, a, at, m.cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.neighbors, m.matrix, m.seq, m.fitted = table, raw, seq, true
	return nil
}

// Refresh updates the neighbors of the subjects with new interactions since
// the last Fit or Refresh, with the same guarantees as [ItemKNN.Refresh].
func (m *UserKNN) Refresh(ctx context.Context) error {
	m.mu.RLock()
	fitted, since := m.fitted, m.seq
	m.mu.RUnlock()
	if !fitted {
		return m.Fit(ctx)
	}
	changed, _ := m.store.Changes(m.cfg.Relation, since)
	if len(changed) == 0 {
		return nil
	}
	a, at, raw, seq, err := subjectMatrices(m.store, m.cfg)
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
	m.neighbors, m.matrix, m.seq = table, raw, seq
	return nil
}

// Neighbors returns the most similar subjects of a subject.
func (m *UserKNN) Neighbors(subject recoant.Ref) []recoant.Scored {
	if subject.Kind != m.cfg.Relation.Subject {
		return nil
	}
	i, ok := m.store.Index(subject)
	if !ok {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if int(i) >= len(m.neighbors) {
		return nil
	}
	return toScored(m.store, subject.Kind, m.neighbors[i])
}

// Recommend answers subject→object requests (what similar subjects
// interacted with) and subject→subject requests (similar subjects).
func (m *UserKNN) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rel := m.cfg.Relation
	if req.Subject.Kind != rel.Subject || (req.Target != rel.Object && req.Target != rel.Subject) {
		return nil, ErrUnsupported
	}
	si, ok := m.store.Index(req.Subject)
	if !ok {
		return nil, nil
	}
	scores := make(map[uint32]float64)
	m.mu.RLock()
	var neighbors []Neighbor
	if int(si) < len(m.neighbors) {
		neighbors = m.neighbors[si]
	}
	for _, n := range neighbors {
		if req.Target == rel.Subject {
			scores[n.Index] = float64(n.Sim)
			continue
		}
		if ctx.Err() != nil {
			m.mu.RUnlock()
			return nil, ctx.Err()
		}
		idx, vals := m.matrix.Row(int(n.Index))
		for k, j := range idx {
			scores[j] += float64(n.Sim) * float64(vals[k])
		}
	}
	m.mu.RUnlock()
	seen := func(uint32) bool { return false }
	if req.ExcludeSeen && req.Target == rel.Object {
		history := m.store.Row(rel, si, req.Time)
		seen = func(j uint32) bool { _, ok := sparse.Get(history, j); return ok }
	}
	return rank(m.store, req, scores, seen), nil
}
