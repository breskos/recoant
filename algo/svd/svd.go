// Package svd implements PureSVD (Cremonesi, Koren and Turrin, 2010): a
// truncated singular value decomposition of the interaction matrix,
// optionally centered (PCA).
//
// The decomposition R ≈ U·Σ·Vᵀ is computed with a randomized algorithm
// that never forms a dense matrix, so it scales to millions of users. A
// subject is folded in from its live history in the store: u = Vᵀ·r, and
// objects are scored by V·u. New interactions therefore change the
// recommendations immediately, also for subjects that were not part of
// training; new objects need a new Fit.
package svd

import (
	"cmp"
	"context"
	"fmt"
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

// Config configures PureSVD.
type Config struct {
	// Relation is the relation to learn from, e.g. user→product.
	Relation store.Relation `json:"relation"`
	// Factors is the rank of the decomposition; default 64.
	Factors int `json:"factors,omitempty"`
	// Oversample is the number of extra random directions; default 10.
	Oversample int `json:"oversample,omitempty"`
	// PowerIters is the number of power iterations; default 2.
	PowerIters int `json:"power_iters,omitempty"`
	// Center subtracts each object's mean strength first, which turns the
	// decomposition into a PCA. Scores then include the object mean.
	Center bool `json:"center,omitempty"`
	// Seed makes the randomized decomposition reproducible.
	Seed uint64 `json:"seed,omitempty"`
	// Workers is the number of parallel workers; default the number of CPUs.
	Workers int `json:"workers,omitempty"`
}

// PureSVD is a truncated SVD recommender. It is safe for concurrent use.
type PureSVD struct {
	store *store.Store
	cfg   Config

	mu        sync.RWMutex
	items     *embed.Matrix // n×k: V
	sim       *embed.Matrix // n×k: V·Σ, for similar objects
	simNorms  []float64
	users     *embed.Matrix // m×k: U·Σ
	userNorms []float64
	mean      []float64 // object means if centered
	meanProj  []float64 // Vᵀ·mean
	sigma     []float64
	fitTime   time.Time
}

// New returns an untrained model that learns from s.
func New(s *store.Store, cfg Config) (*PureSVD, error) {
	if cfg.Relation.Subject == "" || cfg.Relation.Object == "" {
		return nil, fmt.Errorf("svd: relation %q incomplete", cfg.Relation)
	}
	cfg.Factors = cmp.Or(cfg.Factors, 64)
	cfg.Oversample = cmp.Or(cfg.Oversample, 10)
	cfg.PowerIters = cmp.Or(cfg.PowerIters, 2)
	if cfg.Factors < 0 || cfg.Oversample < 0 || cfg.PowerIters < 0 || cfg.Workers < 0 {
		return nil, fmt.Errorf("svd: negative parameter in %+v", cfg)
	}
	return &PureSVD{store: s, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (m *PureSVD) Config() Config { return m.cfg }

// Fit decomposes the current interaction matrix.
func (m *PureSVD) Fit(ctx context.Context) error {
	now := m.store.Stats().Latest
	r, err := m.store.Matrix(m.cfg.Relation, now)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewPCG(m.cfg.Seed, m.cfg.Seed^0x5bd1e995))
	d, err := linalg.RandomizedSVD(ctx, r, r.Transpose(), linalg.SVDConfig{
		Rank: m.cfg.Factors, Oversample: m.cfg.Oversample, PowerIters: m.cfg.PowerIters,
		Center: m.cfg.Center, Workers: m.cfg.Workers,
	}, rng)
	if err != nil {
		return err
	}
	k := len(d.S)
	items, sim, users := embed.New(r.Cols, k), embed.New(r.Cols, k), embed.New(r.Rows, k)
	for c := range k {
		for j, x := range d.V.Row(c) {
			items.Row(j)[c] = x
			sim.Row(j)[c] = x * d.S[c]
		}
		for i, x := range d.U.Row(c) {
			users.Row(i)[c] = x * d.S[c]
		}
	}
	var meanProj []float64
	if d.Mean != nil {
		meanProj = make([]float64, k)
		for c := range k {
			meanProj[c] = linalg.Dot(d.V.Row(c), d.Mean)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items, m.sim, m.users, m.mean, m.meanProj, m.sigma, m.fitTime = items, sim, users, d.Mean, meanProj, d.S, now
	m.simNorms, m.userNorms = sim.Norms(), users.Norms()
	return nil
}

// SingularValues returns the singular values of the last Fit, largest first.
func (m *PureSVD) SingularValues() []float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]float64(nil), m.sigma...)
}

// userVector folds in a subject from its history: u = Vᵀ·(r − mean).
func (m *PureSVD) userVector(_ uint32, history []sparse.Entry) ([]float64, bool) {
	if len(history) == 0 {
		return nil, false
	}
	u := make([]float64, m.items.K)
	for _, e := range history {
		if int(e.Col) < m.items.N {
			linalg.Axpy(float64(e.Val), m.items.Row(int(e.Col)), u)
		}
	}
	if m.meanProj != nil {
		linalg.Axpy(-1, m.meanProj, u)
	}
	return u, true
}

// Recommend answers subject→object (folded in from the live history),
// object→object and subject→subject requests.
func (m *PureSVD) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Recommend(ctx, factor.View{
		Store: m.store, Rel: m.cfg.Relation,
		Items: m.items, ItemBias: m.mean,
		SimItems: m.sim, SimItemNorms: m.simNorms,
		Users: m.users, UserNorms: m.userNorms,
		UserVector:  m.userVector,
		HistoryTime: m.fitTime,
	}, req)
}
