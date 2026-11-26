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

// Feedback is the meaning of a signal type.
type Feedback string

// Feedback kinds.
const (
	// Implicit is positive evidence of interest: visits, clicks, purchases.
	// More signals add up. It is the default.
	Implicit Feedback = "implicit"
	// Rating is an explicit judgment on a scale with a neutral point, such
	// as 1–5 stars with neutral 3. Above the neutral point it is positive,
	// below negative. A new rating replaces the old one.
	Rating Feedback = "rating"
	// Negative is evidence against interest: dislike, pass, skip, hide,
	// return, block.
	Negative Feedback = "negative"
)

// Rule describes the signals of one type.
type Rule struct {
	// Weight scales the signal. For implicit and negative feedback it is the
	// strength per signal (a positive number also for negative feedback);
	// for ratings it scales the centered rating in the preference.
	Weight float64 `json:"weight"`
	// Transform maps the signal's value. Default: constant for implicit and
	// negative feedback, linear for ratings.
	Transform Transform `json:"transform"`
	// Feedback is the meaning of the type; default Implicit.
	Feedback Feedback `json:"feedback,omitempty"`
	// Relation names the relation the signals belong to. Empty means the
	// unnamed relation between the subject's and the object's kind.
	Relation string `json:"relation,omitempty"`
	// Channel is the store channel the signals go to. Default "positive",
	// "negative" or "rating" according to Feedback.
	Channel string `json:"channel,omitempty"`
	// Scale is the [min, max] range of ratings; values are clipped to it.
	// Optional.
	Scale []float64 `json:"scale,omitempty"`
	// Neutral is the neutral rating, e.g. 3 on a 1–5 scale.
	Neutral float64 `json:"neutral,omitempty"`
}

// FeedbackKind returns the feedback kind with the default applied.
func (r Rule) FeedbackKind() Feedback {
	if r.Feedback == "" {
		return Implicit
	}
	return r.Feedback
}

// ChannelName returns the channel with the default applied.
func (r Rule) ChannelName() string {
	if r.Channel != "" {
		return r.Channel
	}
	switch r.FeedbackKind() {
	case Negative:
		return "negative"
	case Rating:
		return "rating"
	}
	return "positive"
}

// value returns the value stored for a signal value v: the weighted,
// transformed value for implicit and negative feedback, the (clipped,
// transformed) rating for ratings.
func (r Rule) value(v float64) float64 {
	if r.FeedbackKind() != Rating {
		return r.Apply(v)
	}
	if len(r.Scale) == 2 {
		v = math.Max(r.Scale[0], math.Min(r.Scale[1], v))
	}
	t := r.Transform
	if t.Kind == "" {
		t.Kind = Linear
	}
	return t.Apply(v)
}

// Apply returns Weight · Transform(v).
func (r Rule) Apply(v float64) float64 { return r.Weight * r.Transform.Apply(v) }

// Validate reports an error for an invalid weight, transform, feedback kind
// or scale.
func (r Rule) Validate() error {
	if math.IsNaN(r.Weight) || math.IsInf(r.Weight, 0) {
		return fmt.Errorf("%w: weight %v", ErrInvalidModel, r.Weight)
	}
	switch r.FeedbackKind() {
	case Implicit:
	case Negative:
		if r.Weight < 0 {
			return fmt.Errorf("%w: negative feedback needs a positive weight (its sign is implied), got %v", ErrInvalidModel, r.Weight)
		}
	case Rating:
		if len(r.Scale) != 0 && (len(r.Scale) != 2 || !(r.Scale[0] < r.Scale[1])) {
			return fmt.Errorf("%w: scale must be [min, max] with min < max, got %v", ErrInvalidModel, r.Scale)
		}
		if len(r.Scale) == 2 && (r.Neutral < r.Scale[0] || r.Neutral > r.Scale[1]) {
			return fmt.Errorf("%w: neutral %v outside scale %v", ErrInvalidModel, r.Neutral, r.Scale)
		}
		if math.IsNaN(r.Neutral) || math.IsInf(r.Neutral, 0) {
			return fmt.Errorf("%w: neutral %v", ErrInvalidModel, r.Neutral)
		}
	default:
		return fmt.Errorf("%w: unknown feedback %q", ErrInvalidModel, r.Feedback)
	}
	return r.Transform.Validate()
}

// RelationSpec declares a named relation: the kinds it connects and,
// optionally, its own half-life and channel settings.
type RelationSpec struct {
	Subject recoant.Kind `json:"subject"`
	Object  recoant.Kind `json:"object"`
	// HalfLife overrides the model's half-life for the relation's channels.
	HalfLife time.Duration `json:"half_life,omitempty"`
	// Channels overrides the model's channel settings for this relation.
	Channels map[string]Channel `json:"channels,omitempty"`
	// Retention is how long a pair of the relation is kept after its last
	// signal; the store's Expire drops older pairs. Zero keeps them
	// forever.
	Retention time.Duration `json:"retention,omitempty"`
}

// Model turns signals into interaction strength.
type Model struct {
	// Rules describes the known signal types.
	Rules map[recoant.SignalType]Rule `json:"rules"`
	// Default is used for signal types without a rule. If nil, such signals
	// are ignored.
	Default *Rule `json:"default,omitempty"`
	// HalfLife is the time after which a signal counts half, for channels
	// without their own half-life. Zero disables decay.
	HalfLife time.Duration `json:"half_life"`
	// Channels holds settings for channels by name, for all relations;
	// channels without settings get defaults (see [Channel]). A relation can
	// override them in its [RelationSpec].
	Channels map[string]Channel `json:"channels,omitempty"`
	// Relations declares named relations. Signals for a declared relation
	// must connect its kinds.
	Relations map[string]RelationSpec `json:"relations,omitempty"`
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

// Validate reports an error if a rule, channel or relation is invalid, if
// the rules of a channel disagree, or if the half-life is negative.
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
	for name, c := range m.Channels {
		if name == "" {
			return fmt.Errorf("%w: channel with empty name", ErrInvalidModel)
		}
		if err := c.validate(); err != nil {
			return fmt.Errorf("channel %q: %w", name, err)
		}
	}
	for name, k := range m.Relations {
		if name == "" || k.Subject == "" || k.Object == "" {
			return fmt.Errorf("%w: relation %q needs a name and both kinds", ErrInvalidModel, name)
		}
		if k.HalfLife < 0 {
			return fmt.Errorf("%w: relation %q has a negative half-life", ErrInvalidModel, name)
		}
		if k.Retention < 0 {
			return fmt.Errorf("%w: relation %q has a negative retention", ErrInvalidModel, name)
		}
		for ch, c := range k.Channels {
			if err := c.validate(); err != nil {
				return fmt.Errorf("relation %q channel %q: %w", name, ch, err)
			}
		}
	}
	for _, rel := range m.RelationNames() {
		for _, name := range m.ChannelNames(rel) {
			if _, err := m.resolve(rel, name); err != nil {
				return err
			}
		}
	}
	return nil
}

// RuleFor returns the rule for a signal type, or the default rule. ok is
// false if the model ignores the type.
func (m Model) RuleFor(t recoant.SignalType) (Rule, bool) { return m.rule(t) }

func (m Model) rule(t recoant.SignalType) (Rule, bool) {
	r, ok := m.Rules[t]
	if !ok {
		if m.Default == nil {
			return Rule{}, false
		}
		r = *m.Default
	}
	return r, true
}

// Route says where a signal goes: its relation name (empty for the unnamed
// relation), its channel and the value stored there (see [Rule]). ok is
// false if the model ignores the signal's type.
func (m Model) Route(s recoant.Signal) (relation, channel string, value float64, ok bool) {
	r, ok := m.rule(s.Type)
	if !ok {
		return "", "", 0, false
	}
	return r.Relation, r.ChannelName(), r.value(s.Value), true
}

// Weight returns the signed contribution of a signal to the preference,
// without decay: the weighted value for implicit feedback, its negative for
// negative feedback, and Weight·(rating − Neutral) for ratings. ok is false
// if the model ignores the signal's type.
func (m Model) Weight(s recoant.Signal) (w float64, ok bool) {
	r, ok := m.rule(s.Type)
	if !ok {
		return 0, false
	}
	v := r.value(s.Value)
	switch r.FeedbackKind() {
	case Negative:
		return -v, true
	case Rating:
		return r.Weight * (v - r.Neutral), true
	}
	return v, true
}

// Rate returns the decay rate λ per second (ln 2 / HalfLife), or 0 without
// decay.
func (m Model) Rate() float64 { return Rate(m.HalfLife) }

// Decay returns the factor a signal of the given age is multiplied with,
// with the model's half-life.
func (m Model) Decay(age time.Duration) float64 {
	return math.Exp(-m.Rate() * age.Seconds())
}

// Strength returns the signed contribution of a signal at time now, with
// the decay of its channel. Signals from the future relative to now are not
// amplified beyond their weight.
func (m Model) Strength(s recoant.Signal, now time.Time) float64 {
	w, ok := m.Weight(s)
	if !ok {
		return 0
	}
	r, _ := m.rule(s.Type)
	spec, _ := m.resolve(r.Relation, r.ChannelName())
	age := now.Sub(s.Time)
	if age < 0 {
		age = 0
	}
	return w * math.Exp(-spec.Rate*age.Seconds())
}

// Rate returns the decay rate λ per second for a half-life, or 0 if the
// half-life is not positive.
func Rate(halfLife time.Duration) float64 {
	if halfLife <= 0 {
		return 0
	}
	return math.Ln2 / halfLife.Seconds()
}
