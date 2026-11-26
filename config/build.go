package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/algo/bpr"
	"github.com/breskos/recoant/algo/knn"
	"github.com/breskos/recoant/algo/mf"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/algo/svd"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// SignalModel returns the signal model the configuration describes.
func (c *Config) SignalModel() (signal.Model, error) {
	m := signal.Model{
		Rules:     make(map[recoant.SignalType]signal.Rule, len(c.Signals)),
		HalfLife:  time.Duration(c.HalfLife),
		Relations: make(map[string]signal.RelationSpec, len(c.Relations)),
	}
	for name, r := range c.Relations {
		spec := signal.RelationSpec{
			Subject: recoant.Kind(r.Subject), Object: recoant.Kind(r.Object),
			HalfLife: time.Duration(r.HalfLife),
		}
		if len(r.Channels) > 0 {
			spec.Channels = make(map[string]signal.Channel, len(r.Channels))
			for ch, cc := range r.Channels {
				spec.Channels[ch] = signal.Channel{Aggregate: signal.Aggregate(cc.Aggregate), HalfLife: time.Duration(cc.HalfLife), NoDecay: cc.NoDecay}
			}
		}
		m.Relations[name] = spec
	}
	for name, s := range c.Signals {
		r := signal.Rule{
			Weight:   1,
			Feedback: signal.Feedback(s.Feedback),
			Relation: s.Relation,
			Channel:  s.Channel,
			Scale:    s.Scale,
			Neutral:  s.Neutral,
		}
		if s.Weight != nil {
			r.Weight = *s.Weight
		}
		if s.Transform != nil {
			r.Transform = signal.Transform{Kind: signal.TransformKind(s.Transform.Kind), Param: s.Transform.Param}
		}
		m.Rules[recoant.SignalType(name)] = r
	}
	if err := m.Validate(); err != nil {
		return m, fmt.Errorf("%w: signals: %w", ErrInvalid, err)
	}
	return m, nil
}

// Relation returns the store relation of a configured relation name.
func (c *Config) Relation(name string) store.Relation {
	r := c.Relations[name]
	return store.Relation{Name: name, Subject: recoant.Kind(r.Subject), Object: recoant.Kind(r.Object)}
}

// strict decodes params into v, rejecting unknown fields.
func strict(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

type popularityParams struct {
	HalfLife Duration `json:"half_life,omitempty"`
}

// buildModel creates the recommender of a model declaration.
func (c *Config) buildModel(name string, s *store.Store, sm signal.Model) (Trainable, error) {
	d := c.Models[name]
	path := "models." + name + ".params"
	rel := c.Relation(d.Relation)
	fail := func(err error) (Trainable, error) { return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, path, err) }
	switch d.Algorithm {
	case "popularity":
		var p popularityParams
		if err := strict(d.Params, &p); err != nil {
			return fail(err)
		}
		m, err := popularity.New(sm, popularity.Config{HalfLife: time.Duration(p.HalfLife)})
		if err != nil {
			return fail(err)
		}
		m.SetSeen(s)
		return online{m}, nil
	case "item-knn", "user-knn":
		var cfg knn.Config
		if err := strict(d.Params, &cfg); err != nil {
			return fail(err)
		}
		cfg.Relation = rel
		if d.Algorithm == "item-knn" {
			m, err := knn.NewItemKNN(s, cfg)
			if err != nil {
				return fail(err)
			}
			return m, nil
		}
		m, err := knn.NewUserKNN(s, cfg)
		if err != nil {
			return fail(err)
		}
		return m, nil
	case "pure-svd":
		var cfg svd.Config
		if err := strict(d.Params, &cfg); err != nil {
			return fail(err)
		}
		cfg.Relation = rel
		m, err := svd.New(s, cfg)
		if err != nil {
			return fail(err)
		}
		return m, nil
	case "mf", "funk-svd", "biased-mf", "svd++":
		var cfg mf.Config
		if err := strict(d.Params, &cfg); err != nil {
			return fail(err)
		}
		cfg.Relation, cfg.Channel = rel, d.Channel
		switch d.Algorithm {
		case "funk-svd":
			cfg.Variant = mf.Funk
		case "biased-mf":
			cfg.Variant = mf.Biased
		case "svd++":
			cfg.Variant = mf.SVDPlusPlus
		}
		m, err := mf.New(s, cfg)
		if err != nil {
			return fail(err)
		}
		return m, nil
	case "als":
		var cfg als.Config
		if err := strict(d.Params, &cfg); err != nil {
			return fail(err)
		}
		cfg.Relation = rel
		m, err := als.New(s, cfg)
		if err != nil {
			return fail(err)
		}
		return m, nil
	default: // bpr; Validate rejects unknown algorithms
		var cfg bpr.Config
		if err := strict(d.Params, &cfg); err != nil {
			return fail(err)
		}
		cfg.Relation = rel
		m, err := bpr.New(s, cfg)
		if err != nil {
			return fail(err)
		}
		return m, nil
	}
}
