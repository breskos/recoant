// Package signal turns signals into interaction strength.
//
// A [Model] assigns each signal type a [Rule]: a weight and a [Transform] of
// the signal's value. A visit may count 1, a purchase 10, and a dwell time
// may count more the longer it is, but with diminishing returns. The model
// also defines how fast old signals lose influence (HalfLife).
//
// The strength a signal adds to a (subject, object) pair at time now is
//
//	Weight · Transform(Value) · 2^(−(now − Time) / HalfLife)
package signal

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/breskos/recoant"
)

// ErrInvalidModel is returned by [Model.Validate].
var ErrInvalidModel = errors.New("signal: invalid model")

// TransformKind selects how a signal's value is turned into a factor.
type TransformKind string

// Transforms. Negative values are treated as 0 by all transforms except
// Linear.
const (
	// Constant ignores the value and returns 1. It is the default.
	Constant TransformKind = "constant"
	// Linear returns the value.
	Linear TransformKind = "linear"
	// Log returns ln(1 + value).
	Log TransformKind = "log"
	// Saturate returns 1 − e^(−value/Param): about 0.63 at Param, close to 1
	// beyond 3·Param. Suits dwell times.
	Saturate TransformKind = "saturate"
	// Threshold returns 1 if value ≥ Param and 0 otherwise, e.g. to drop
	// bounces with a dwell time below a few seconds.
	Threshold TransformKind = "threshold"
	// Cap returns min(value, Param).
	Cap TransformKind = "cap"
)

// Transform maps a signal value to a factor.
type Transform struct {
	Kind  TransformKind `json:"kind,omitempty"`
	Param float64       `json:"param,omitempty"`
}

// Apply returns the factor for value v.
func (t Transform) Apply(v float64) float64 {
	if t.Kind != Linear && v < 0 {
		v = 0
	}
	switch t.Kind {
	case Linear:
		return v
	case Log:
		return math.Log1p(v)
	case Saturate:
		return -math.Expm1(-v / t.Param)
	case Threshold:
		if v >= t.Param {
			return 1
		}
		return 0
	case Cap:
		return math.Min(v, t.Param)
	default:
		return 1
	}
}

// Validate reports an error for an unknown kind or an invalid parameter.
func (t Transform) Validate() error {
	switch t.Kind {
	case "", Constant, Linear, Log:
		return nil
	case Saturate, Cap:
		if !(t.Param > 0) || math.IsInf(t.Param, 0) {
			return fmt.Errorf("%w: transform %s needs a positive param, got %v", ErrInvalidModel, t.Kind, t.Param)
		}
		return nil
	case Threshold:
		if math.IsNaN(t.Param) || math.IsInf(t.Param, 0) {
			return fmt.Errorf("%w: transform threshold needs a finite param", ErrInvalidModel)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown transform %q", ErrInvalidModel, t.Kind)
	}
}

// Rule weights the signals of one type.
type Rule struct {
	Weight    float64   `json:"weight"`
	Transform Transform `json:"transform"`
}

// Apply returns Weight · Transform(v).
func (r Rule) Apply(v float64) float64 { return r.Weight * r.Transform.Apply(v) }

// Validate reports an error for a non-finite weight or an invalid transform.
func (r Rule) Validate() error {
	if math.IsNaN(r.Weight) || math.IsInf(r.Weight, 0) {
		return fmt.Errorf("%w: weight %v", ErrInvalidModel, r.Weight)
	}
	return r.Transform.Validate()
}

// Model turns signals into interaction strength.
type Model struct {
	// Rules weights the known signal types.
	Rules map[recoant.SignalType]Rule `json:"rules"`
	// Default is used for signal types without a rule. If nil, such signals
	// are ignored.
	Default *Rule `json:"default,omitempty"`
	// HalfLife is the time after which a signal counts half. Zero disables
	// decay.
	HalfLife time.Duration `json:"half_life"`
}

// DefaultModel returns a model for the common signal types: visit 1,
// click 2, dwell up to 3 (saturating with a scale of 60 seconds), cart 5 and
// purchase 10, with a half-life of 30 days. Other types are ignored.
func DefaultModel() Model {
	return Model{
		Rules: map[recoant.SignalType]Rule{
			recoant.Visit:    {Weight: 1},
			recoant.Click:    {Weight: 2},
			recoant.Dwell:    {Weight: 3, Transform: Transform{Kind: Saturate, Param: 60}},
			recoant.Cart:     {Weight: 5},
			recoant.Purchase: {Weight: 10},
		},
		HalfLife: 30 * 24 * time.Hour,
	}
}

// Validate reports an error if a rule is invalid or the half-life negative.
func (m Model) Validate() error {
	for typ, r := range m.Rules {
		if typ == "" {
			return fmt.Errorf("%w: rule for empty signal type", ErrInvalidModel)
		}
		if err := r.Validate(); err != nil {
			return fmt.Errorf("rule %q: %w", typ, err)
		}
	}
	if m.Default != nil {
		if err := m.Default.Validate(); err != nil {
			return fmt.Errorf("default rule: %w", err)
		}
	}
	if m.HalfLife < 0 {
		return fmt.Errorf("%w: negative half-life %v", ErrInvalidModel, m.HalfLife)
	}
	return nil
}

// Weight returns the strength of a signal without decay. ok is false if the
// model ignores the signal's type.
func (m Model) Weight(s recoant.Signal) (w float64, ok bool) {
	r, ok := m.Rules[s.Type]
	if !ok {
		if m.Default == nil {
			return 0, false
		}
		r = *m.Default
	}
	return r.Apply(s.Value), true
}

// Rate returns the decay rate λ per second (ln 2 / HalfLife), or 0 without
// decay.
func (m Model) Rate() float64 { return Rate(m.HalfLife) }

// Decay returns the factor a signal of the given age is multiplied with.
func (m Model) Decay(age time.Duration) float64 {
	return math.Exp(-m.Rate() * age.Seconds())
}

// Strength returns the strength of a signal at time now, including decay.
// Signals from the future relative to now are not amplified beyond their
// weight.
func (m Model) Strength(s recoant.Signal, now time.Time) float64 {
	w, ok := m.Weight(s)
	if !ok {
		return 0
	}
	age := now.Sub(s.Time)
	if age < 0 {
		age = 0
	}
	return w * m.Decay(age)
}

// Rate returns the decay rate λ per second for a half-life, or 0 if the
// half-life is not positive.
func Rate(halfLife time.Duration) float64 {
	if halfLife <= 0 {
		return 0
	}
	return math.Ln2 / halfLife.Seconds()
}
