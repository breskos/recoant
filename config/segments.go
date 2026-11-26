package config

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/segment"
)

// segmentAttribute returns the attribute that holds the segments of a kind.
func (c *Config) segmentAttribute(kind string) string {
	return cmp.Or(c.Segments[kind].Attribute, "segment")
}

func (c *Config) validateSegments() error {
	for _, kind := range sortedKeys(c.Segments) {
		sg := c.Segments[kind]
		path := "segments." + kind
		k, ok := c.Kinds[kind]
		if !ok {
			return invalid(path, "unknown kind")
		}
		if err := (segment.KMeansConfig{K: sg.K, Iterations: sg.Iterations, BatchSize: sg.BatchSize, Restarts: sg.Restarts}).Validate(); err != nil {
			return invalid(path, "%v", err)
		}
		if len(sg.Features) == 0 {
			return invalid(path+".features", "at least one feature is needed")
		}
		if _, ok := k.Attributes[c.segmentAttribute(kind)]; ok {
			return invalid(path+".attribute", "%q is already an attribute of %s; choose another name", c.segmentAttribute(kind), kind)
		}
		var model Model
		if sg.Model != "" {
			if model, ok = c.Models[sg.Model]; !ok {
				return invalid(path+".model", "unknown model %q", sg.Model)
			}
			if !slices.Contains(factorModels, model.Algorithm) {
				return invalid(path+".model", "must be a factor model (%v), got %s", factorModels, model.Algorithm)
			}
		}
		rel := c.Relations[model.Relation]
		for i, f := range sg.Features {
			fp := fmt.Sprintf("%s.features[%d]", path, i)
			switch {
			case slices.Contains(sg.Features[:i], f):
				return invalid(fp, "%q is listed twice", f)
			case f == segment.Taste || f == segment.Appearance:
				side := rel.Subject
				if f == segment.Appearance {
					side = rel.Object
				}
				if sg.Model == "" || side != kind {
					return invalid(fp, "%s needs a model whose relation has %s as %s", f, kind, map[string]string{segment.Taste: "subject", segment.Appearance: "object"}[f])
				}
			default:
				a, ok := k.Attributes[f]
				if !ok {
					return invalid(fp, "unknown attribute %s.%s", kind, f)
				}
				if a.Sensitive && !sg.AllowSensitive {
					return invalid(fp, "%s.%s is sensitive; set allow_sensitive to cluster on it", kind, f)
				}
			}
		}
		for _, f := range sortedKeys(sg.Weights) {
			if !slices.Contains(sg.Features, f) || !(sg.Weights[f] > 0) {
				return invalid(path+".weights."+f, "must be a positive weight of a listed feature")
			}
		}
		if sg.Relation != "" {
			r, ok := c.Relations[sg.Relation]
			if !ok {
				return invalid(path+".relation", "unknown relation %q", sg.Relation)
			}
			if r.Subject != kind {
				return invalid(path+".relation", "%q has subject kind %q, not %s", sg.Relation, r.Subject, kind)
			}
		}
	}
	return nil
}

// segmentConfig returns the segment configuration of a kind.
func (c *Config) segmentConfig(kind string) segment.Config {
	sg := c.Segments[kind]
	cfg := segment.Config{
		KMeansConfig:   segment.KMeansConfig{K: sg.K, Iterations: sg.Iterations, BatchSize: sg.BatchSize, Seed: sg.Seed, Restarts: sg.Restarts},
		Kind:           recoant.Kind(kind),
		AllowSensitive: sg.AllowSensitive,
		Ignore:         []string{c.segmentAttribute(kind)},
	}
	for _, f := range sg.Features {
		cfg.Features = append(cfg.Features, segment.Feature{Name: f, Weight: sg.Weights[f]})
	}
	rel := sg.Relation
	if m := c.Models[sg.Model]; rel == "" && sg.Model != "" && c.Relations[m.Relation].Subject == kind {
		rel = m.Relation
	}
	if rel != "" {
		cfg.Relation = c.Relation(rel)
	}
	return cfg
}

// buildSegmenters creates the segmenters of all kinds.
func (e *Engine) buildSegmenters() error {
	for _, kind := range sortedKeys(e.cfg.Segments) {
		var roles segment.Roles
		if m, ok := e.models[e.cfg.Segments[kind].Model].(segment.Roles); ok {
			roles = m
		}
		s, err := segment.New(e.store, e.attrs, roles, e.cfg.segmentConfig(kind))
		if err != nil {
			return fmt.Errorf("%w: segments.%s: %w", ErrInvalid, kind, err)
		}
		if e.segmenters == nil {
			e.segmenters = make(map[string]*segment.Segmenter)
		}
		e.segmenters[kind] = s
	}
	return nil
}

// fitSegments fits the segmenters and writes every entity's segment into
// the segment attribute, so that filters can use it.
func (e *Engine) fitSegments(ctx context.Context) error {
	for _, kind := range sortedKeys(e.segmenters) {
		s := e.segmenters[kind]
		if err := s.Fit(ctx); err != nil {
			return fmt.Errorf("segments of %s: %w", kind, err)
		}
		name := e.cfg.segmentAttribute(kind)
		for i := range e.store.Len(recoant.Kind(kind)) {
			ref, ok := e.store.Ref(recoant.Kind(kind), uint32(i))
			if !ok {
				continue
			}
			if j, ok := s.Segment(ref); ok {
				_ = e.attrs.Set(ref, name, attr.Cat(fmt.Sprintf("s%d", j)))
			} else {
				_, _ = e.attrs.Unset(ref, name)
			}
		}
	}
	return nil
}

// Segmenter returns the segmenter of a kind.
func (e *Engine) Segmenter(kind recoant.Kind) (*segment.Segmenter, bool) {
	s, ok := e.segmenters[string(kind)]
	return s, ok
}
