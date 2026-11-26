// Package bpr implements Bayesian personalized ranking with matrix
// factorization (Rendle et al., 2009) for implicit feedback.
//
// BPR learns to rank: for a subject u, an object i it likes (positive
// preference) and an object j it does not, it pushes the score of i above
// the score of j. j is an object u rejected (negative preference) in half of
// the samples if u rejected any — a hard negative — and a random object
// without positive preference otherwise. Scores are b_i + p_u·q_i.
// Training is stochastic gradient descent over sampled triples.
package bpr

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/embed"
	"github.com/breskos/recoant/internal/factor"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

// Config configures BPR.
type Config struct {
	// Relation is the relation to learn from, e.g. user→product.
	Relation store.Relation `json:"relation"`
	// Factors is the number of latent factors; default 32.
	Factors int `json:"factors,omitempty"`
	// Epochs is the number of passes; each samples as many triples as there
	// are interactions. Default 30.
	Epochs int `json:"epochs,omitempty"`
	// LearnRate is the SGD step size; default 0.05.
	LearnRate float64 `json:"learn_rate,omitempty"`
	// Reg is the L2 regularization; default 0.01.
	Reg float64 `json:"reg,omitempty"`
	// Seed makes training reproducible.
	Seed uint64 `json:"seed,omitempty"`
}

// BPR is a pairwise ranking factorization. It is safe for concurrent use.
type BPR struct {
	store *store.Store
	cfg   Config

	mu        sync.RWMutex
	p, q      *embed.Matrix
	bias      []float64
	userNorms []float64
	fitTime   time.Time
	fitted    bool
}

// New returns an untrained model that learns from s.
func New(s *store.Store, cfg Config) (*BPR, error) {
	if cfg.Relation.Subject == "" || cfg.Relation.Object == "" {
		return nil, fmt.Errorf("bpr: relation %q incomplete", cfg.Relation)
	}
	cfg.Factors = cmp.Or(cfg.Factors, 32)
	cfg.Epochs = cmp.Or(cfg.Epochs, 30)
	cfg.LearnRate = cmp.Or(cfg.LearnRate, 0.05)
	cfg.Reg = cmp.Or(cfg.Reg, 0.01)
	if cfg.Factors < 0 || cfg.Epochs < 0 || cfg.LearnRate < 0 || cfg.Reg < 0 ||
		math.IsNaN(cfg.LearnRate) || math.IsInf(cfg.LearnRate, 0) || math.IsNaN(cfg.Reg) || math.IsInf(cfg.Reg, 0) {
		return nil, fmt.Errorf("bpr: invalid parameter in %+v", cfg)
	}
	return &BPR{store: s, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (m *BPR) Config() Config { return m.cfg }

// Fit trains the model on the current interactions in the store.
func (m *BPR) Fit(ctx context.Context) error {
	now := m.store.Stats().Latest
	r, err := m.store.Matrix(m.cfg.Relation, now)
	if err != nil {
		return err
	}
	cfg := m.cfg
	rng := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x85ebca6b))
	p := embed.Random(rng, r.Rows, cfg.Factors, 0.1)
	q := embed.Random(rng, r.Cols, cfg.Factors, 0.1)
	bias := make([]float64, r.Cols)
	// positives lists the positive entries; negatives the rejected objects
	// of each subject.
	var positives []int
	rowOf := make([]int32, r.NNZ())
	negatives := make([][]uint32, r.Rows)
	for u := range r.Rows {
		for k := r.Indptr[u]; k < r.Indptr[u+1]; k++ {
			rowOf[k] = int32(u)
			if r.Values[k] > 0 {
				positives = append(positives, k)
			} else if r.Values[k] < 0 {
				negatives[u] = append(negatives[u], r.Indices[k])
			}
		}
	}
	lr, reg := cfg.LearnRate, cfg.Reg
	for range cfg.Epochs {
		if err := ctx.Err(); err != nil {
			return err
		}
		for range positives {
			e := positives[rng.IntN(len(positives))]
			u := int(rowOf[e])
			i := int(r.Indices[e])
			var j int
			if neg := negatives[u]; len(neg) > 0 && rng.IntN(2) == 0 {
				j = int(neg[rng.IntN(len(neg))])
			} else {
				j = rng.IntN(r.Cols)
				for tries := 0; tries < 10 && r.At(u, uint32(j)) > 0; tries++ {
					j = rng.IntN(r.Cols)
				}
				if r.At(u, uint32(j)) > 0 {
					continue // no object without positive preference found
				}
			}
			pu, qi, qj := p.Row(u), q.Row(i), q.Row(j)
			x := bias[i] - bias[j]
			for c := range pu {
				x += pu[c] * (qi[c] - qj[c])
			}
			g := 1 / (1 + math.Exp(x)) // σ(−x)
			bias[i] += lr * (g - reg*bias[i])
			bias[j] += lr * (-g - reg*bias[j])
			for c := range pu {
				puc, qic, qjc := pu[c], qi[c], qj[c]
				pu[c] += lr * (g*(qic-qjc) - reg*puc)
				qi[c] += lr * (g*puc - reg*qic)
				qj[c] += lr * (-g*puc - reg*qjc)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.p, m.q, m.bias, m.userNorms, m.fitTime, m.fitted = p, q, bias, p.Norms(), now, true
	return nil
}

func (m *BPR) userVector(si uint32, _ []sparse.Entry) ([]float64, bool) {
	if int(si) >= m.p.N {
		return nil, false
	}
	return m.p.Row(int(si)), true
}

// Recommend answers subject→object, object→object and subject→subject
// requests. Subjects need to be part of a Fit.
func (m *BPR) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Recommend(ctx, m.view(), req)
}

// Interest returns the predicted interest of every subject in the object,
// from the subjects' trained vectors; NaN where a vector is missing. It
// scores the reverse direction of reciprocal matching.
func (m *BPR) Interest(subjects []recoant.Ref, object recoant.Ref) []float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Interest(m.view(), subjects, object)
}

// view returns a snapshot of the model; the caller holds m.mu.
func (m *BPR) view() factor.View {
	v := factor.View{Store: m.store, Rel: m.cfg.Relation, UserVector: m.userVector, HistoryTime: m.fitTime}
	if m.fitted {
		v.Items, v.ItemBias, v.Users, v.UserNorms = m.q, m.bias, m.p, m.userNorms
	}
	return v
}

// Taste returns the trained subject vector of ref (what it is drawn to);
// false if it has none.
func (m *BPR) Taste(ref recoant.Ref) ([]float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Taste(m.view(), ref)
}

// Appearance returns the object vector of ref (how it is perceived);
// false if it has none.
func (m *BPR) Appearance(ref recoant.Ref) ([]float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return factor.Appearance(m.view(), ref)
}
