// Package match recommends pairs where both sides must agree: dating,
// mentoring, job matching, tenants and flats.
//
// A [Reciprocal] recommender combines two directions:
//
//	match(u, v) = combine( interest(u → v), interest(v → u) )
//
// The forward direction comes from any recommender for the relation, e.g.
// ALS on `user —likes→ user`. The reverse direction comes from a [Scorer]:
// the same factor model for a relation between entities of one kind
// (its subject vectors are the taste, its object vectors the appearance of
// each entity), or a model of the reverse relation, e.g. `job —recruits→
// user` for `user —applies→ job`. All factor models (ALS, BPR, the MF
// variants and PureSVD) are Scorers.
//
// Both interests are calibrated to [0, 1] and combined with a harmonic or
// geometric mean, the minimum or the product. Pairs below a minimum in
// either direction and pairs where the candidate rejected the subject are
// left out. Exposure balancing lowers the score of candidates that already
// receive much attention, so that attention spreads and more mutual
// matches happen.
package match

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/store"
)

// Scorer predicts the interest of subjects in an object. Entries are NaN
// where the model has no vector.
type Scorer interface {
	Interest(subjects []recoant.Ref, object recoant.Ref) []float64
}

// Combine selects how the two directions are combined.
type Combine string

// Combinations of the calibrated interests a (forward) and b (reverse).
const (
	// Harmonic is 2ab/(a+b): high only if both are high. It is the default.
	Harmonic Combine = "harmonic"
	// Geometric is √(ab): forgives an imbalance a bit more.
	Geometric Combine = "geometric"
	// Min is min(a, b): the weaker side decides.
	Min Combine = "min"
	// Product is ab: the probability that both agree, if independent.
	Product Combine = "product"
)

// Calibration maps raw model scores to [0, 1].
type Calibration string

// Calibrations.
const (
	// Clamp cuts scores to [0, 1]. It suits ALS, whose scores estimate a
	// preference between 0 and 1. It is the default.
	Clamp Calibration = "clamp"
	// Sigmoid maps scores with 1/(1+e^−x). It suits BPR and the MF
	// variants, whose scores are unbounded.
	Sigmoid Calibration = "sigmoid"
)

// ErrConfig is returned for invalid configurations.
var ErrConfig = errors.New("match: invalid configuration")

// Config configures a reciprocal recommender.
type Config struct {
	// Relation is the forward relation: subjects are recommended objects
	// of its object kind.
	Relation store.Relation `json:"relation"`
	// Combine is the combination; default Harmonic.
	Combine Combine `json:"combine,omitempty"`
	// Calibrate maps both directions to [0, 1]; default Clamp.
	Calibrate Calibration `json:"calibrate,omitempty"`
	// MinEach is the minimum calibrated interest in each direction.
	MinEach float64 `json:"min_each,omitempty"`
	// ExposureBalancing β in [0, 1] divides a candidate's score by
	// (1 + n/n̄)^β, where n is the number of subjects with a positive
	// preference for the candidate and n̄ its mean over all candidates
	// with one. 0 turns it off.
	ExposureBalancing float64 `json:"exposure_balancing,omitempty"`
	// Pool is the number of forward candidates that are rescored; default
	// 20·k, at least 200.
	Pool int `json:"pool,omitempty"`
}

// Validate reports an error for an incomplete relation or a setting out
// of range. Empty settings take their defaults.
func (c Config) Validate() error {
	switch {
	case c.Relation.Subject == "" || c.Relation.Object == "":
		return fmt.Errorf("%w: relation %q incomplete", ErrConfig, c.Relation)
	case c.Combine != "" && c.Combine != Harmonic && c.Combine != Geometric && c.Combine != Min && c.Combine != Product:
		return fmt.Errorf("%w: combine must be harmonic, geometric, min or product, got %q", ErrConfig, c.Combine)
	case c.Calibrate != "" && c.Calibrate != Clamp && c.Calibrate != Sigmoid:
		return fmt.Errorf("%w: calibrate must be clamp or sigmoid, got %q", ErrConfig, c.Calibrate)
	case !(c.MinEach >= 0 && c.MinEach <= 1):
		return fmt.Errorf("%w: min_each must be in [0, 1], got %v", ErrConfig, c.MinEach)
	case !(c.ExposureBalancing >= 0 && c.ExposureBalancing <= 1):
		return fmt.Errorf("%w: exposure_balancing must be in [0, 1], got %v", ErrConfig, c.ExposureBalancing)
	case c.Pool < 0:
		return fmt.Errorf("%w: pool must not be negative, got %d", ErrConfig, c.Pool)
	}
	return nil
}

// Reciprocal recommends objects that the subject likes and that like the
// subject back. It is safe for concurrent use.
type Reciprocal struct {
	store   *store.Store
	forward recoant.Recommender
	reverse Scorer
	cfg     Config

	mu       sync.RWMutex
	exposure []float64 // by object index: 1/(1+n/n̄)^β
}

// New returns a reciprocal recommender. forward answers requests in the
// forward relation; reverse scores the interest of the candidates in the
// subject.
func New(s *store.Store, forward recoant.Recommender, reverse Scorer, cfg Config) (*Reciprocal, error) {
	if s == nil || forward == nil || reverse == nil {
		return nil, fmt.Errorf("%w: store, forward and reverse are required", ErrConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg.Combine = cmp.Or(cfg.Combine, Harmonic)
	cfg.Calibrate = cmp.Or(cfg.Calibrate, Clamp)
	return &Reciprocal{store: s, forward: forward, reverse: reverse, cfg: cfg}, nil
}

// Config returns the configuration with defaults applied.
func (r *Reciprocal) Config() Config { return r.cfg }

// Fit counts, for exposure balancing, how many subjects have a positive
// preference for each object. Without exposure balancing it does nothing.
func (r *Reciprocal) Fit(ctx context.Context) error {
	if r.cfg.ExposureBalancing == 0 {
		return nil
	}
	m, err := r.store.Matrix(r.cfg.Relation, r.store.Stats().Latest)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	counts := make([]float64, m.Cols)
	for n, j := range m.Indices {
		if m.Values[n] > 0 {
			counts[j]++
		}
	}
	sum, n := 0.0, 0
	for _, c := range counts {
		if c > 0 {
			sum, n = sum+c, n+1
		}
	}
	exposure := make([]float64, len(counts))
	for j, c := range counts {
		exposure[j] = 1
		if n > 0 {
			exposure[j] = math.Pow(1+c/(sum/float64(n)), -r.cfg.ExposureBalancing)
		}
	}
	r.mu.Lock()
	r.exposure = exposure
	r.mu.Unlock()
	return nil
}

// Refresh recounts the exposure; see [Reciprocal.Fit].
func (r *Reciprocal) Refresh(ctx context.Context) error { return r.Fit(ctx) }

// Recommend answers subject→object requests of the forward relation with
// the best mutual matches. The request's filters and exclusions apply to
// the forward candidates. Requests in other directions, e.g. similar
// objects, are passed to the forward recommender unchanged.
func (r *Reciprocal) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Subject.Kind != r.cfg.Relation.Subject || req.Target != r.cfg.Relation.Object {
		return r.forward.Recommend(ctx, req)
	}
	fwd := req
	fwd.K = r.cfg.Pool
	if fwd.K == 0 {
		fwd.K = max(20*req.K, 200)
	}
	cands, err := r.forward.Recommend(ctx, fwd)
	if err != nil || len(cands) == 0 {
		return nil, err
	}
	refs := make([]recoant.Ref, len(cands))
	for i, c := range cands {
		refs[i] = c.Ref
	}
	back := r.reverse.Interest(refs, req.Subject)
	r.mu.RLock()
	exposure := r.exposure
	r.mu.RUnlock()
	tk := topk.New(req.K)
	for i, c := range cands {
		a, b := r.calibrate(c.Score), r.calibrate(back[i])
		if a < r.cfg.MinEach || b < r.cfg.MinEach {
			continue
		}
		if !req.IncludeRejected && r.store.Rejected(c.Ref, req.Subject) {
			continue
		}
		score := r.combine(a, b)
		if score <= 0 {
			continue
		}
		if j, ok := r.store.Index(c.Ref); ok && int(j) < len(exposure) {
			score *= exposure[j]
		}
		tk.Push(uint32(i), score)
	}
	sorted := tk.Sorted()
	out := make([]recoant.Scored, len(sorted))
	for n, e := range sorted {
		out[n] = recoant.Scored{Ref: cands[e.Index].Ref, Score: e.Score}
	}
	return out, nil
}

// calibrate maps a raw score to [0, 1]; NaN (no vector) becomes 0.
func (r *Reciprocal) calibrate(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return 0
	case r.cfg.Calibrate == Sigmoid:
		return 1 / (1 + math.Exp(-x))
	}
	return min(max(x, 0), 1)
}

func (r *Reciprocal) combine(a, b float64) float64 {
	switch r.cfg.Combine {
	case Geometric:
		return math.Sqrt(a * b)
	case Min:
		return min(a, b)
	case Product:
		return a * b
	}
	if a+b == 0 {
		return 0
	}
	return 2 * a * b / (a + b)
}
