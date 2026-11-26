// Package popularity recommends the most popular objects of a kind.
//
// Popularity is the sum of the weighted signals an object received —
// negative feedback and ratings below the neutral point count down — with an
// optional half-life: without decay it ranks by all-time popularity, with a
// short half-life (hours or days) it ranks what is trending now. It is the
// baseline every personalized model must beat, and the fallback for
// subjects without history (cold start).
//
// It learns online ([Popularity.Observe]); batch training replays signals
// ([Popularity.Fit]).
package popularity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/dict"
	"github.com/breskos/recoant/internal/fdecay"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/signal"
)

// ErrNoSeen is returned for a request with ExcludeSeen when no [recoant.Seen]
// source was set.
var ErrNoSeen = errors.New("popularity: ExcludeSeen needs a Seen source, see SetSeen")

// Config configures a popularity model.
type Config struct {
	// HalfLife is the time after which a signal counts half. Zero ranks by
	// all-time popularity. It replaces the half-life of the signal model.
	HalfLife time.Duration `json:"half_life"`
}

// Popularity ranks objects by their weighted, time-decayed signal count.
// It is safe for concurrent use.
type Popularity struct {
	mu     sync.RWMutex
	model  signal.Model
	cfg    Config
	clock  fdecay.Clock
	dicts  map[recoant.Kind]*dict.Dictionary
	scores map[recoant.Kind][]float64
	latest time.Time
	seen   recoant.Seen
}

// New returns an empty model. Signals are weighted with the rules of m; the
// decay comes from cfg.
func New(m signal.Model, cfg Config) (*Popularity, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if cfg.HalfLife < 0 {
		return nil, fmt.Errorf("popularity: negative half-life %v", cfg.HalfLife)
	}
	return &Popularity{
		model:  m,
		cfg:    cfg,
		clock:  fdecay.Clock{Rate: signal.Rate(cfg.HalfLife)},
		dicts:  make(map[recoant.Kind]*dict.Dictionary),
		scores: make(map[recoant.Kind][]float64),
	}, nil
}

// SetSeen sets the source used for [recoant.Request.ExcludeSeen], usually
// the [store.Store] that receives the same signals. If it also implements
// [recoant.Rejecter], rejected objects are excluded unless
// [recoant.Request.IncludeRejected] is set.
func (p *Popularity) SetSeen(s recoant.Seen) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = s
}

// Observe learns from one signal. It implements [recoant.Observer].
func (p *Popularity) Observe(sig recoant.Signal) error {
	if err := sig.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.observe(sig)
	return nil
}

func (p *Popularity) observe(sig recoant.Signal) {
	if sig.Time.After(p.latest) {
		p.latest = sig.Time
	}
	w, ok := p.model.Weight(sig)
	if !ok || w == 0 {
		return
	}
	if p.clock.NeedsRebase(sig.Time) {
		f := p.clock.Rebase(sig.Time)
		for _, sc := range p.scores {
			for i := range sc {
				sc[i] *= f
			}
		}
	}
	k := sig.Object.Kind
	d := p.dicts[k]
	if d == nil {
		d = dict.New()
		p.dicts[k] = d
	}
	i := d.Intern(sig.Object.ID)
	sc := p.scores[k]
	if int(i) >= len(sc) {
		sc = append(sc, 0)
	}
	sc[i] += w * p.clock.Scale(sig.Time)
	p.scores[k] = sc
}

// Fit learns from signals in order (batch training as a replayed stream).
func (p *Popularity) Fit(ctx context.Context, sigs []recoant.Signal) error {
	for i, sig := range sigs {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := p.Observe(sig); err != nil {
			return fmt.Errorf("signal %d: %w", i, err)
		}
	}
	return nil
}

// Score returns the popularity of an object at time now, or 0.
func (p *Popularity) Score(ref recoant.Ref, now time.Time) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d := p.dicts[ref.Kind]
	if d == nil {
		return 0
	}
	i, ok := d.Lookup(ref.ID)
	if !ok {
		return 0
	}
	return p.scores[ref.Kind][i] * p.clock.At(now)
}

// Recommend returns the most popular objects of the target kind. The
// ranking is the same for every subject except for exclusions.
func (p *Popularity) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if req.ExcludeSeen && p.seen == nil {
		return nil, ErrNoSeen
	}
	d := p.dicts[req.Target]
	if d == nil {
		return nil, nil
	}
	sc := p.scores[req.Target]
	var rejecter recoant.Rejecter
	if r, ok := p.seen.(recoant.Rejecter); ok && !req.IncludeRejected {
		rejecter = r
	}
	excluded := make(map[uint32]struct{}, len(req.Exclude)+1)
	exclude := func(r recoant.Ref) {
		if r.Kind == req.Target {
			if i, ok := d.Lookup(r.ID); ok {
				excluded[i] = struct{}{}
			}
		}
	}
	exclude(req.Subject)
	for _, r := range req.Exclude {
		exclude(r)
	}
	tk := topk.New(req.K)
	for i, s := range sc {
		if i%65536 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if _, ok := excluded[uint32(i)]; ok || s <= 0 {
			continue
		}
		if req.Filter != nil || req.ExcludeSeen || rejecter != nil {
			ref := recoant.Ref{Kind: req.Target, ID: d.ID(uint32(i))}
			if req.Filter != nil && !req.Filter(ref) {
				continue
			}
			if req.ExcludeSeen && p.seen.Has(req.Subject, ref) {
				continue
			}
			if rejecter != nil && rejecter.Rejected(req.Subject, ref) {
				continue
			}
		}
		tk.Push(uint32(i), s)
	}
	now := req.Time
	if now.IsZero() {
		now = p.latest
	}
	f := p.clock.At(now)
	items := tk.Sorted()
	out := make([]recoant.Scored, len(items))
	for k, it := range items {
		out[k] = recoant.Scored{Ref: recoant.Ref{Kind: req.Target, ID: d.ID(it.Index)}, Score: it.Score * f}
	}
	return out, nil
}
