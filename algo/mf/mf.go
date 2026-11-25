// Package mf implements matrix factorization for explicit feedback such as
// star ratings, as made popular by the Netflix Prize: Funk-SVD, biased MF
// and SVD++ (Koren, 2008), trained with stochastic gradient descent.
//
// The prediction for subject u and object i is
//
//	Funk:   r̂ = q_i·p_u
//	Biased: r̂ = μ + b_u + b_i + q_i·p_u
//	SVD++:  r̂ = μ + b_u + b_i + q_i·(p_u + |N(u)|^-½ Σ_{j∈N(u)} y_j)
//
// where N(u) are the objects u interacted with. The training targets are
// the strengths in the store, so use a signal model that maps a rating to
// its value: weight 1, [signal.Linear] transform and no half-life. The
// store sums signals, so every pair should be rated once.
//
// SVD++ folds a subject in from its live history, so it can recommend for
// subjects that were not part of training. Funk and biased MF need a Fit.
package mf

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

// Variant selects the model.
type Variant string

// Variants.
const (
	Funk        Variant = "funk"
	Biased      Variant = "biased" // default
	SVDPlusPlus Variant = "svd++"
)

// Config configures a matrix factorization.
type Config struct {
	// Relation is the relation to learn from, e.g. user→movie.
	Relation store.Relation `json:"relation"`
	// Variant is the model; default Biased.
	Variant Variant `json:"variant,omitempty"`
	// Factors is the number of latent factors; default 32.
	Factors int `json:"factors,omitempty"`
	// Epochs is the number of passes over the data; default 20.
	Epochs int `json:"epochs,omitempty"`
	// LearnRate is the SGD step size; default 0.007.
	LearnRate float64 `json:"learn_rate,omitempty"`
	// Reg is the L2 regularization of factors and biases; default 0.02.
	Reg float64 `json:"reg,omitempty"`
	// InitStd is the standard deviation of the initial factors; default 0.1.
	InitStd float64 `json:"init_std,omitempty"`
	// Min and Max clip predictions if Max > Min, e.g. 1 and 5 for stars.
	Min float64 `json:"min,omitempty"`
	Max float64 `json:"max,omitempty"`
	// Seed makes training reproducible.
	Seed uint64 `json:"seed,omitempty"`
}

func (c Config) withDefaults() (Config, error) {
	if c.Relation.Subject == "" || c.Relation.Object == "" {
		return c, fmt.Errorf("mf: relation %q incomplete", c.Relation)
	}
	c.Variant = cmp.Or(c.Variant, Biased)
	switch c.Variant {
	case Funk, Biased, SVDPlusPlus:
	default:
		return c, fmt.Errorf("mf: unknown variant %q", c.Variant)
	}
	c.Factors = cmp.Or(c.Factors, 32)
	c.Epochs = cmp.Or(c.Epochs, 20)
	c.LearnRate = cmp.Or(c.LearnRate, 0.007)
	c.Reg = cmp.Or(c.Reg, 0.02)
	c.InitStd = cmp.Or(c.InitStd, 0.1)
	for _, v := range []float64{c.LearnRate, c.Reg, c.InitStd, c.Min, c.Max} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return c, fmt.Errorf("mf: non-finite parameter in %+v", c)
		}
	}
	if c.Factors < 0 || c.Epochs < 0 || c.LearnRate < 0 || c.Reg < 0 || c.InitStd < 0 {
		return c, fmt.Errorf("mf: negative parameter in %+v", c)
	}
	return c, nil
}

// MF is a matrix factorization model. It is safe for concurrent use.
type MF struct {
	store *store.Store
	cfg   Config

	mu        sync.RWMutex
	global    float64
	bu, bi    []float64
	p, q, y   *embed.Matrix // y only for SVD++
	userNorms []float64
	fitTime   time.Time
	fitted    bool
}

// New returns an untrained model that learns from s.
func New(s *store.Store, cfg Config) (*MF, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &MF{store: s, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (m *MF) Config() Config { return m.cfg }

// Fit trains the model on the current strengths in the store.
func (m *MF) Fit(ctx context.Context) error {
	now := m.store.Stats().Latest
	r, err := m.store.Matrix(m.cfg.Relation, now)
	if err != nil {
		return err
	}
	cfg := m.cfg
	rng := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x2545f4914f6cdd1d))
	k := cfg.Factors
	p := embed.Random(rng, r.Rows, k, cfg.InitStd)
	q := embed.Random(rng, r.Cols, k, cfg.InitStd)
	var y *embed.Matrix
	if cfg.Variant == SVDPlusPlus {
		y = embed.New(r.Cols, k)
	}
	bu, bi := make([]float64, r.Rows), make([]float64, r.Cols)
	var global float64
	if cfg.Variant != Funk && r.NNZ() > 0 {
		for _, v := range r.Values {
			global += float64(v)
		}
		global /= float64(r.NNZ())
	}
	lr, reg := cfg.LearnRate, cfg.Reg
	pu := make([]float64, k)  // effective user vector
	z := make([]float64, k)   // implicit part (SVD++)
	acc := make([]float64, k) // gradient of z (SVD++)
	var order []int
	for range cfg.Epochs {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, u := range rng.Perm(r.Rows) {
			idx, vals := r.Row(u)
			if len(idx) == 0 {
				continue
			}
			clear(z)
			norm := 0.0
			if y != nil {
				norm = 1 / math.Sqrt(float64(len(idx)))
				for _, j := range idx {
					for c, x := range y.Row(int(j)) {
						z[c] += norm * x
					}
				}
				clear(acc)
			}
			pr := p.Row(u)
			order = order[:0]
			for k := range idx {
				order = append(order, k)
			}
			rng.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
			for _, e := range order {
				i := int(idx[e])
				qi := q.Row(i)
				for c := range pu {
					pu[c] = pr[c] + z[c]
				}
				pred := global + bu[u] + bi[i]
				for c, x := range qi {
					pred += x * pu[c]
				}
				err := float64(vals[e]) - pred
				if cfg.Variant != Funk {
					bu[u] += lr * (err - reg*bu[u])
					bi[i] += lr * (err - reg*bi[i])
				}
				for c := range qi {
					qc := qi[c]
					qi[c] += lr * (err*pu[c] - reg*qc)
					pr[c] += lr * (err*qc - reg*pr[c])
					acc[c] += err * qc
				}
			}
			if y != nil {
				for _, j := range idx {
					yr := y.Row(int(j))
					for c := range yr {
						yr[c] += lr * (norm*acc[c] - reg*yr[c])
					}
				}
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.global, m.bu, m.bi, m.p, m.q, m.y = global, bu, bi, p, q, y
	m.userNorms, m.fitTime, m.fitted = p.Norms(), now, true
	return nil
}

// userVector returns p_u (+ the implicit part for SVD++, from the live
// history). Subjects without a trained vector have one only with SVD++.
func (m *MF) userVector(si uint32, history []sparse.Entry) ([]float64, bool) {
	u := make([]float64, m.q.K)
	known := int(si) < m.p.N
	if known {
		copy(u, m.p.Row(int(si)))
	}
	if m.y != nil && len(history) > 0 {
		norm := 1 / math.Sqrt(float64(len(history)))
		for _, e := range history {
			if int(e.Col) < m.y.N {
				for c, x := range m.y.Row(int(e.Col)) {
					u[c] += norm * x
				}
			}
		}
		return u, true
	}
	return u, known
}

// Predict returns the predicted value for a subject–object pair. Unknown
// subjects and objects fall back to the global mean and the known biases.
// ok is false before the first Fit.
func (m *MF) Predict(subject, object recoant.Ref) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.fitted {
		return 0, false
	}
	pred := m.global
	si, sok := m.store.Index(subject)
	oi, ook := m.store.Index(object)
	ook = ook && int(oi) < m.q.N
	if sok && int(si) < len(m.bu) {
		pred += m.bu[si]
	}
	if ook {
		pred += m.bi[oi]
		var history []sparse.Entry
		if sok && m.y != nil {
			history = m.store.Row(m.cfg.Relation, si, m.fitTime)
		}
		if sok {
			if u, ok := m.userVector(si, history); ok {
				for c, x := range m.q.Row(int(oi)) {
					pred += x * u[c]
				}
			}
		}
	}
	if m.cfg.Max > m.cfg.Min {
		pred = math.Max(m.cfg.Min, math.Min(m.cfg.Max, pred))
	}
	return pred, true
}

// Recommend answers subject→object, object→object and subject→subject
// requests. Scores rank by predicted value without the global mean and the
// subject bias, which are equal for all objects.
func (m *MF) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v := factor.View{Store: m.store, Rel: m.cfg.Relation, UserVector: m.userVector, HistoryTime: m.fitTime}
	if m.fitted {
		v.Items, v.Users, v.UserNorms = m.q, m.p, m.userNorms
		if m.cfg.Variant != Funk {
			v.ItemBias = m.bi
		}
	}
	return factor.Recommend(ctx, v, req)
}
