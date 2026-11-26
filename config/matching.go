package config

import (
	"fmt"
	"slices"

	"github.com/breskos/recoant/match"
)

// factorModels are the algorithms that score the reverse direction of
// reciprocal matching.
var factorModels = []string{"pure-svd", "mf", "funk-svd", "biased-mf", "svd++", "als", "bpr"}

func (c *Config) validateShared(path string, m Model) error {
	if len(m.Shared) == 0 {
		return nil
	}
	if m.Algorithm != "als" {
		return invalid(path+".shared", "only als supports shared relations")
	}
	own := c.Relations[m.Relation]
	for i, name := range m.Shared {
		r, ok := c.Relations[name]
		switch {
		case !ok:
			return invalid(fmt.Sprintf("%s.shared[%d]", path, i), "unknown relation %q", name)
		case name == m.Relation || slices.Contains(m.Shared[:i], name):
			return invalid(fmt.Sprintf("%s.shared[%d]", path, i), "relation %q is listed twice", name)
		case r.Subject != own.Subject:
			return invalid(fmt.Sprintf("%s.shared[%d]", path, i), "relation %q has subject kind %q, the model's relation %q", name, r.Subject, own.Subject)
		}
	}
	return nil
}

func (c *Config) validateMatching() error {
	for _, rel := range sortedKeys(c.Matching) {
		m := c.Matching[rel]
		path := "matching." + rel
		r, ok := c.Relations[rel]
		if !ok {
			return invalid(path, "unknown relation")
		}
		switch m.Mode {
		case "", "one_way":
			continue
		case "reciprocal":
		default:
			return invalid(path+".mode", "must be one_way or reciprocal, got %q", m.Mode)
		}
		if err := c.matchConfig(rel).Validate(); err != nil {
			return invalid(path, "%v", err)
		}
		if m.ReverseModel == "" {
			if r.Subject != r.Object {
				return invalid(path+".reverse_model", "needed because %q connects different kinds (%s → %s)", rel, r.Subject, r.Object)
			}
			for _, name := range sortedKeys(c.Models) {
				if mod := c.Models[name]; mod.Relation == rel && !slices.Contains(factorModels, mod.Algorithm) {
					return invalid("models."+name+".algorithm", "%s cannot score the reverse direction of reciprocal %q; use a factor model or set matching.%s.reverse_model", mod.Algorithm, rel, rel)
				}
			}
			continue
		}
		rm, ok := c.Models[m.ReverseModel]
		if !ok {
			return invalid(path+".reverse_model", "unknown model %q", m.ReverseModel)
		}
		if !slices.Contains(factorModels, rm.Algorithm) {
			return invalid(path+".reverse_model", "must be a factor model (%v), got %s", factorModels, rm.Algorithm)
		}
		if back := c.Relations[rm.Relation]; back.Subject != r.Object || back.Object != r.Subject {
			return invalid(path+".reverse_model", "must learn a relation %s → %s, %q learns %q", r.Object, r.Subject, m.ReverseModel, rm.Relation)
		}
	}
	return nil
}

// reciprocal reports whether a relation is matched reciprocally.
func (c *Config) reciprocal(rel string) bool { return c.Matching[rel].Mode == "reciprocal" }

// matchConfig returns the match configuration of a relation.
func (c *Config) matchConfig(rel string) match.Config {
	m := c.Matching[rel]
	return match.Config{
		Relation: c.Relation(rel), Combine: match.Combine(m.Combine), Calibrate: match.Calibration(m.Calibrate),
		MinEach: m.MinEach, ExposureBalancing: m.ExposureBalancing, Pool: m.Pool,
	}
}
