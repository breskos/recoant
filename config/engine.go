package config

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/store"
)

// ErrUnknown is returned for unknown model or serving names.
var ErrUnknown = errors.New("config: unknown name")

// Trainable is a recommender that can be trained.
type Trainable interface {
	recoant.Recommender
	Fit(ctx context.Context) error
}

// refresher is implemented by models with incremental updates.
type refresher interface {
	Refresh(ctx context.Context) error
}

// online wraps a popularity model, which learns from every signal and has
// nothing to fit.
type online struct{ *popularity.Popularity }

func (online) Fit(context.Context) error { return nil }

// deleter is implemented by models that keep their own IDs.
type deleter interface {
	Delete(ref recoant.Ref) bool
}

// Engine holds the store, the attributes and the models of a
// configuration.
type Engine struct {
	cfg    *Config
	store  *store.Store
	attrs  *attr.Store
	models map[string]Trainable
	names  []string
}

// Build creates the store and all models.
func (c *Config) Build() (*Engine, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	sm, err := c.SignalModel()
	if err != nil {
		return nil, err
	}
	s, err := store.New(sm)
	if err != nil {
		return nil, err
	}
	attrs, err := attr.New(c.Schema())
	if err != nil {
		return nil, fmt.Errorf("%w: kinds: %w", ErrInvalid, err)
	}
	e := &Engine{cfg: c, store: s, attrs: attrs, models: make(map[string]Trainable), names: sortedKeys(c.Models)}
	for _, name := range e.names {
		m, err := c.buildModel(name, s, sm)
		if err != nil {
			return nil, err
		}
		e.models[name] = m
	}
	return e, nil
}

// Config returns the configuration.
func (e *Engine) Config() *Config { return e.cfg }

// Store returns the store.
func (e *Engine) Store() *store.Store { return e.store }

// Attributes returns the attribute store.
func (e *Engine) Attributes() *attr.Store { return e.attrs }

// Apply records an attribute event: set, update or delete.
func (e *Engine) Apply(ev attr.Event) error { return e.attrs.Apply(ev) }

// Delete removes a user or entity: its signals in the store, its scores in
// popularity models, and, unless the privacy section disables it, its
// attributes. Vectors of trained models stay in memory until the next Fit
// or Refresh, but are never recommended again. Delete reports whether
// anything was removed.
func (e *Engine) Delete(ref recoant.Ref) bool {
	found := e.store.Delete(ref)
	for _, name := range e.names {
		if d, ok := e.models[name].(deleter); ok && d.Delete(ref) {
			found = true
		}
	}
	if p := e.cfg.Privacy; (p == nil || p.DeleteAttributesWithEntity == nil || *p.DeleteAttributesWithEntity) && e.attrs.Delete(ref) {
		found = true
	}
	return found
}

// Expire drops the pairs of relations with a retention (privacy.retention)
// whose last signal is older than the retention at time now (zero: the
// latest signal). It returns the number of pairs dropped. Refresh updates
// the models afterwards.
func (e *Engine) Expire(now time.Time) int { return e.store.Expire(now) }

// ExportAttributes returns the attributes of a reference for profiles and
// exports. Sensitive attributes are left out unless privacy.export_sensitive
// is set.
func (e *Engine) ExportAttributes(ref recoant.Ref) map[string]attr.Value {
	return e.attrs.Attributes(ref, e.cfg.Privacy != nil && e.cfg.Privacy.ExportSensitive)
}

// Model returns a model by name.
func (e *Engine) Model(name string) (Trainable, bool) {
	m, ok := e.models[name]
	return m, ok
}

// Add records a signal in the store and in the online models.
func (e *Engine) Add(sig recoant.Signal) error {
	if err := e.store.Add(sig); err != nil {
		return err
	}
	for _, name := range e.names {
		if o, ok := e.models[name].(online); ok {
			if err := o.Observe(sig); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddBatch records signals in order and stops at the first error.
func (e *Engine) AddBatch(ctx context.Context, sigs []recoant.Signal) error {
	for i, sig := range sigs {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := e.Add(sig); err != nil {
			return fmt.Errorf("signal %d: %w", i, err)
		}
	}
	return nil
}

// Fit trains all models. Models whose relation or channel has no signals
// yet are skipped.
func (e *Engine) Fit(ctx context.Context) error {
	for _, name := range e.names {
		if err := e.models[name].Fit(ctx); err != nil && !noData(err) {
			return fmt.Errorf("model %s: %w", name, err)
		}
	}
	return nil
}

// Refresh updates the models that support incremental updates and fits
// the others.
func (e *Engine) Refresh(ctx context.Context) error {
	for _, name := range e.names {
		m := e.models[name]
		var err error
		if r, ok := m.(refresher); ok {
			err = r.Refresh(ctx)
		} else {
			err = m.Fit(ctx)
		}
		if err != nil && !noData(err) {
			return fmt.Errorf("model %s: %w", name, err)
		}
	}
	return nil
}

// noData reports errors of models without data yet.
func noData(err error) bool {
	return errors.Is(err, store.ErrUnknownRelation) || errors.Is(err, store.ErrUnknownChannel)
}

// Request returns the request a serving declaration makes for a subject.
// k overrides the configured number of results if positive; where adds
// attribute conditions to the configured filters.
func (e *Engine) Request(serving string, subject recoant.Ref, k int, where ...recoant.Condition) (recoant.Request, error) {
	s, ok := e.cfg.Serving[serving]
	if !ok {
		return recoant.Request{}, fmt.Errorf("%w: serving %q", ErrUnknown, serving)
	}
	target := recoant.Kind(s.Target)
	if target == "" {
		target = recoant.Kind(e.cfg.Relations[e.cfg.Models[s.Model].Relation].Object)
	}
	if k <= 0 {
		k = s.K
	}
	if k <= 0 {
		k = 10
	}
	var conds []recoant.Condition
	if len(s.Filters)+len(where) > 0 {
		conds = append(append(conds, s.Filters...), where...)
	}
	return recoant.Request{Subject: subject, Target: target, K: k, ExcludeSeen: s.ExcludeSeen, IncludeRejected: s.IncludeRejected, Where: conds}, nil
}

// Recommend answers a request of a serving declaration, restricted by its
// filters and the additional conditions where. If the model returns
// nothing (e.g. for a new subject), the fallback model answers.
func (e *Engine) Recommend(ctx context.Context, serving string, subject recoant.Ref, k int, where ...recoant.Condition) ([]recoant.Scored, error) {
	req, err := e.Request(serving, subject, k, where...)
	if err != nil {
		return nil, err
	}
	s := e.cfg.Serving[serving]
	res, err := attr.Filtered(e.models[s.Model], e.attrs).Recommend(ctx, req)
	if err != nil || len(res) > 0 || s.Fallback == "" {
		return res, err
	}
	return attr.Filtered(e.models[s.Fallback], e.attrs).Recommend(ctx, req)
}
