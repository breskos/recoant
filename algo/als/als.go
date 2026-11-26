// Package als implements alternating least squares for implicit feedback
// (Hu, Koren and Volinsky, 2008).
//
// Every pair with a positive preference is a positive with confidence
// c = 1 + Alpha·strength, where the strength comes from the signal model of
// the store: a purchase or a long dwell counts more than a visit. Pairs with
// a negative preference (negative feedback, low ratings) are confident
// negatives with c = 1 + Alpha·|strength|. Pairs without interaction are
// weak negatives. ALS alternates between solving
// all subject vectors with the object vectors fixed and vice versa; each
// solve is an exact least-squares problem.
//
// Subjects are folded in from their live history at request time (one
// small linear solve), so new interactions change the recommendations
// immediately. Refresh re-solves the subjects and objects that changed in
// the store since the last training.
package als

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/embed"
	"github.com/breskos/recoant/internal/factor"
	"github.com/breskos/recoant/internal/linalg"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

// Config configures ALS.
type Config struct {
	// Relation is the relation to learn from, e.g. user→product.
	Relation store.Relation `json:"relation"`
	// Factors is the number of latent factors; default 32.
	Factors int `json:"factors,omitempty"`
	// Iterations is the number of alternations; default 10.
	Iterations int `json:"iterations,omitempty"`
	// Reg is the L2 regularization λ; default 0.1.
	Reg float64 `json:"reg,omitempty"`
	// Alpha scales strengths into confidence: c = 1 + Alpha·strength;
	// default 1.
	Alpha float64 `json:"alpha,omitempty"`
	// Seed makes training reproducible.
	Seed uint64 `json:"seed,omitempty"`
	// Workers is the number of parallel workers; default the number of CPUs.
	Workers int `json:"workers,omitempty"`
	// Shared lists further relations with the same subject kind whose
	// signals also train the subject vectors (collective factorization):
	// a user's taste learns from likes and purchases alike. Recommendations
	// are made for Relation only. With Shared, Refresh fits.
	Shared []store.Relation `json:"shared,omitempty"`
	// SharedWeight scales the strengths of the shared relations; default 1.
	SharedWeight float64 `json:"shared_weight,omitempty"`
}

// ALS is an implicit-feedback factorization. It is safe for concurrent use.
type ALS struct {
	store *store.Store
	cfg   Config

	mu        sync.RWMutex
	x, y      *embed.Matrix // subject and object vectors (y of Relation)
	all       *embed.Matrix // object vectors of Relation and Shared, stacked
	yty       []float64     // Yᵀ·Y of all, k×k
	userNorms []float64
	blocks    []block // column blocks of Relation and Shared in all
	seq       uint64
	fitTime   time.Time
	fitted    bool
}

// block is the place of a relation's objects in the stacked object vectors.
type block struct {
	rel          store.Relation
	offset, cols uint32
	scale        float32
}

// New returns an untrained model that learns from s.
func New(s *store.Store, cfg Config) (*ALS, error) {
	if cfg.Relation.Subject == "" || cfg.Relation.Object == "" {
		return nil, fmt.Errorf("als: relation %q incomplete", cfg.Relation)
	}
	cfg.Factors = cmp.Or(cfg.Factors, 32)
	cfg.Iterations = cmp.Or(cfg.Iterations, 10)
	cfg.Reg = cmp.Or(cfg.Reg, 0.1)
	cfg.Alpha = cmp.Or(cfg.Alpha, 1)
	cfg.SharedWeight = cmp.Or(cfg.SharedWeight, 1)
	if cfg.Factors < 0 || cfg.Iterations < 0 || cfg.Workers < 0 || !(cfg.Reg > 0) || !(cfg.Alpha > 0) ||
		!(cfg.SharedWeight > 0) || math.IsInf(cfg.Reg, 0) || math.IsInf(cfg.Alpha, 0) || math.IsInf(cfg.SharedWeight, 0) {
		return nil, fmt.Errorf("als: invalid parameter in %+v", cfg)
	}
	for _, r := range cfg.Shared {
		if r.Subject != cfg.Relation.Subject || r.Object == "" || r == cfg.Relation {
			return nil, fmt.Errorf("als: shared relation %q must differ from %q and have its subject kind", r, cfg.Relation)
		}
	}
	return &ALS{store: s, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (m *ALS) Config() Config { return m.cfg }

// gram returns Fᵀ·F.
func gram(f *embed.Matrix) []float64 {
	k := f.K
	g := make([]float64, k*k)
	for i := range f.N {
		r := f.Row(i)
		for a := range k {
			if r[a] == 0 {
				continue
			}
			for b := a; b < k; b++ {
				g[a*k+b] += r[a] * r[b]
			}
		}
	}
	for a := range k {
		for b := range a {
			g[a*k+b] = g[b*k+a]
		}
	}
	return g
}

// solver solves one row: (G + λI + Σ (c−1) f fᵀ)·x = Σ c f.
type solver struct {
	k         int
	reg       float64
	alpha     float64
	a, b      []float64
	fixed     *embed.Matrix
	fixedGram []float64
}

func newSolver(k int, reg, alpha float64, fixed *embed.Matrix, g []float64) *solver {
	return &solver{k: k, reg: reg, alpha: alpha, a: make([]float64, k*k), b: make([]float64, k), fixed: fixed, fixedGram: g}
}

func (s *solver) solve(idx []uint32, vals []float32, out []float64) {
	k := s.k
	copy(s.a, s.fixedGram)
	for d := range k {
		s.a[d*k+d] += s.reg
	}
	clear(s.b)
	for n, j := range idx {
		if int(j) >= s.fixed.N {
			continue
		}
		f := s.fixed.Row(int(j))
		v := float64(vals[n])
		c := 1 + s.alpha*math.Abs(v)
		for a := range k {
			ca := (c - 1) * f[a]
			for b := a; b < k; b++ {
				s.a[a*k+b] += ca * f[b]
			}
			if v > 0 {
				s.b[a] += c * f[a]
			}
		}
	}
	for a := range k {
		for b := range a {
			s.a[a*k+b] = s.a[b*k+a]
		}
	}
	if err := linalg.Cholesky(s.a, k); err != nil {
		clear(out) // cannot happen with reg > 0; keep a neutral vector
		return
	}
	linalg.CholeskySolve(s.a, k, s.b)
	copy(out, s.b)
}

// solveRows solves the given rows of m (or all rows if rows is nil) into
// out, with fixed vectors f.
func (m *ALS) solveRows(ctx context.Context, rows []uint32, r *sparse.CSR, f, out *embed.Matrix) error {
	g := gram(f)
	n := r.Rows
	if rows != nil {
		n = len(rows)
	}
	var pool sync.Pool
	pool.New = func() any { return newSolver(m.cfg.Factors, m.cfg.Reg, m.cfg.Alpha, f, g) }
	linalg.Parallel(n, m.cfg.Workers, func(t int) {
		if ctx.Err() != nil {
			return
		}
		i := t
		if rows != nil {
			i = int(rows[t])
		}
		s := pool.Get().(*solver)
		idx, vals := r.Row(i)
		s.solve(idx, vals, out.Row(i))
		pool.Put(s)
	})
	return ctx.Err()
}

// Fit trains the model on the current strengths in the store.
func (m *ALS) Fit(ctx context.Context) error {
	seq := m.store.Seq()
	now := m.store.Stats().Latest
	r, blocks, err := m.matrix(now)
	if err != nil {
		return err
	}
	rt := r.Transpose()
	rng := rand.New(rand.NewPCG(m.cfg.Seed, m.cfg.Seed^0x1b873593))
	k := m.cfg.Factors
	x := embed.Random(rng, r.Rows, k, 0.01)
	all := embed.Random(rng, r.Cols, k, 0.01)
	for range m.cfg.Iterations {
		if err := m.solveRows(ctx, nil, r, all, x); err != nil {
			return err
		}
		if err := m.solveRows(ctx, nil, rt, x, all); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.x, m.all, m.blocks, m.yty, m.userNorms = x, all, blocks, gram(all), x.Norms()
	m.y = &embed.Matrix{N: int(blocks[0].cols), K: k, Data: all.Data[:int(blocks[0].cols)*k]}
	m.seq, m.fitTime, m.fitted = seq, now, true
	return nil
}

// matrix returns the strengths of Relation and, side by side, of the
// shared relations, and the column blocks. Shared relations without
// signals yet are left out.
func (m *ALS) matrix(now time.Time) (*sparse.CSR, []block, error) {
	r, err := m.store.Matrix(m.cfg.Relation, now)
	if err != nil {
		return nil, nil, err
	}
	blocks := []block{{rel: m.cfg.Relation, cols: uint32(r.Cols), scale: 1}}
	if len(m.cfg.Shared) == 0 {
		return r, blocks, nil
	}
	parts := []*sparse.CSR{r}
	rows, cols := r.Rows, r.Cols
	for _, rel := range m.cfg.Shared {
		s, err := m.store.Matrix(rel, now)
		if errors.Is(err, store.ErrUnknownRelation) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		blocks = append(blocks, block{rel: rel, offset: uint32(cols), cols: uint32(s.Cols), scale: float32(m.cfg.SharedWeight)})
		parts = append(parts, s)
		rows, cols = max(rows, s.Rows), cols+s.Cols
	}
	entries := make([][]sparse.Entry, rows)
	for i := range entries {
		for b, p := range parts {
			if i >= p.Rows {
				continue
			}
			idx, vals := p.Row(i)
			for n, j := range idx {
				entries[i] = append(entries[i], sparse.Entry{Col: blocks[b].offset + j, Val: blocks[b].scale * vals[n]})
			}
		}
	}
	return sparse.FromRows(entries, rows, cols, 1), blocks, nil
}

// Refresh re-solves the subjects and objects that changed in the store
// since the last Fit or Refresh (one half-step each), and adds vectors for
// new ones. It fits the model if it was not fitted yet.
func (m *ALS) Refresh(ctx context.Context) error {
	m.mu.RLock()
	fitted, since := m.fitted, m.seq
	m.mu.RUnlock()
	if !fitted || len(m.cfg.Shared) > 0 {
		return m.Fit(ctx)
	}
	subjects, objects := m.store.Changes(m.cfg.Relation, since)
	if len(subjects) == 0 && len(objects) == 0 {
		return nil
	}
	seq := m.store.Seq()
	now := m.store.Stats().Latest
	r, err := m.store.Matrix(m.cfg.Relation, now)
	if err != nil {
		return err
	}
	m.mu.RLock()
	x := &embed.Matrix{N: m.x.N, K: m.x.K, Data: append([]float64(nil), m.x.Data...)}
	y := &embed.Matrix{N: m.y.N, K: m.y.K, Data: append([]float64(nil), m.y.Data...)}
	m.mu.RUnlock()
	x.Grow(r.Rows, rand.New(rand.NewPCG(seq, 1)), 0)
	y.Grow(r.Cols, rand.New(rand.NewPCG(seq, 2)), 0)
	if err := m.solveRows(ctx, objects, r.Transpose(), x, y); err != nil {
		return err
	}
	if err := m.solveRows(ctx, subjects, r, y, x); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.x, m.y, m.all, m.yty, m.userNorms = x, y, y, gram(y), x.Norms()
	m.blocks = []block{{rel: m.cfg.Relation, cols: uint32(r.Cols), scale: 1}}
	m.seq, m.fitTime = seq, now
	return nil
}

// userVector folds a subject in from its history with the object vectors
// fixed: the same solve as in training. With shared relations, the
// subject's rows in them count too.
func (m *ALS) userVector(si uint32, history []sparse.Entry) ([]float64, bool) {
	var idx []uint32
	var vals []float32
	for b, blk := range m.blocks {
		row := history
		if b > 0 {
			row = m.store.Row(blk.rel, si, m.fitTime)
		}
		for _, e := range row {
			if e.Col < blk.cols {
				idx, vals = append(idx, blk.offset+e.Col), append(vals, blk.scale*e.Val)
			}
		}
	}
	if len(idx) == 0 {
		return nil, false
	}
	u := make([]float64, m.cfg.Factors)
	newSolver(m.cfg.Factors, m.cfg.Reg, m.cfg.Alpha, m.all, m.yty).solve(idx, vals, u)
	return u, true
}

// Recommend answers subject→object (folded in from the live history),
// object→object and subject→subject requests.
func (m *ALS) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Recommend(ctx, m.view(), req)
}

// Interest returns the predicted interest of every subject in the object,
// from the subjects' trained vectors; NaN where a vector is missing. It
// scores the reverse direction of reciprocal matching.
func (m *ALS) Interest(subjects []recoant.Ref, object recoant.Ref) []float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Interest(m.view(), subjects, object)
}

// view returns a snapshot of the model; the caller holds m.mu.
func (m *ALS) view() factor.View {
	v := factor.View{Store: m.store, Rel: m.cfg.Relation, UserVector: m.userVector, HistoryTime: m.fitTime}
	if m.fitted {
		v.Items, v.Users, v.UserNorms = m.y, m.x, m.userNorms
	}
	return v
}
