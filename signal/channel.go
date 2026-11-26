package signal

import (
	"fmt"
	"slices"
	"time"
)

// Aggregate says how the values of a channel combine for one pair.
type Aggregate string

// Aggregations.
const (
	// Sum adds the values: more signals, more evidence. Default for
	// implicit and negative feedback.
	Sum Aggregate = "sum"
	// Last keeps the latest value. Default for ratings.
	Last Aggregate = "last"
	// Max keeps the largest (decayed) value, e.g. the deepest funnel stage.
	Max Aggregate = "max"
	// Mean averages the values. It needs NoDecay.
	Mean Aggregate = "mean"
)

// Channel configures a store channel.
type Channel struct {
	// Aggregate combines the values of a pair. Default Last for rating
	// channels, Sum otherwise.
	Aggregate Aggregate `json:"aggregate,omitempty"`
	// HalfLife overrides the model's half-life for this channel.
	HalfLife time.Duration `json:"half_life,omitempty"`
	// NoDecay disables decay. Rating channels do not decay unless they set
	// a HalfLife.
	NoDecay bool `json:"no_decay,omitempty"`
}

func (c Channel) validate() error {
	switch c.Aggregate {
	case "", Sum, Last, Max, Mean:
	default:
		return fmt.Errorf("%w: unknown aggregate %q", ErrInvalidModel, c.Aggregate)
	}
	if c.HalfLife < 0 {
		return fmt.Errorf("%w: negative half-life %v", ErrInvalidModel, c.HalfLife)
	}
	if c.Aggregate == Mean && !c.NoDecay {
		return fmt.Errorf("%w: aggregate mean needs no_decay", ErrInvalidModel)
	}
	return nil
}

// ChannelSpec is a channel with all defaults resolved.
type ChannelSpec struct {
	Name      string
	Feedback  Feedback // of the rules writing to the channel
	Aggregate Aggregate
	Rate      float64 // decay rate per second; 0 without decay
	// Neutral and Weight define the channel's contribution to the
	// preference: Weight·(value − Neutral). Implicit channels have weight 1,
	// negative channels −1, rating channels the weight of their rules.
	Neutral float64
	Weight  float64
}

// RelationNames returns the names of the relations used by the rules or
// declared, sorted. The unnamed relation is "".
func (m Model) RelationNames() []string {
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	for _, r := range m.rules() {
		add(r.Relation)
	}
	for n := range m.Relations {
		add(n)
	}
	slices.Sort(names)
	return names
}

func (m Model) rules() []Rule {
	rules := make([]Rule, 0, len(m.Rules)+1)
	for _, r := range m.Rules {
		rules = append(rules, r)
	}
	if m.Default != nil {
		rules = append(rules, *m.Default)
	}
	return rules
}

// ChannelNames returns the channels of a relation ("" for the unnamed
// relation): those its rules write to and those configured for the
// relation, sorted. [Model.Channels] only provides settings.
func (m Model) ChannelNames(relation string) []string {
	var names []string
	add := func(n string) {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	for _, r := range m.rules() {
		if r.Relation == relation {
			add(r.ChannelName())
		}
	}
	for n := range m.Relations[relation].Channels {
		add(n)
	}
	slices.Sort(names)
	return names
}

// ChannelSpec returns the resolved channel of a relation ("" for the
// unnamed relation). ok is false for a channel that the relation does not
// have, or if its rules disagree (see [Model.Validate]).
func (m Model) ChannelSpec(relation, name string) (ChannelSpec, bool) {
	spec, err := m.resolve(relation, name)
	return spec, err == nil && slices.Contains(m.ChannelNames(relation), name)
}

func (m Model) resolve(relation, name string) (ChannelSpec, error) {
	spec := ChannelSpec{Name: name, Feedback: Implicit, Weight: 1}
	var first *Rule
	for _, r := range m.rules() {
		if r.Relation != relation || r.ChannelName() != name {
			continue
		}
		if first == nil {
			first = &r
			spec.Feedback = r.FeedbackKind()
			continue
		}
		if r.FeedbackKind() != first.FeedbackKind() {
			return spec, fmt.Errorf("%w: channel %q mixes %s and %s feedback", ErrInvalidModel, name, first.FeedbackKind(), r.FeedbackKind())
		}
		if r.FeedbackKind() == Rating && (r.Neutral != first.Neutral || r.Weight != first.Weight) {
			return spec, fmt.Errorf("%w: rating channel %q needs the same neutral and weight in all rules", ErrInvalidModel, name)
		}
	}
	c, ok := m.Relations[relation].Channels[name]
	if !ok {
		c = m.Channels[name]
	}
	switch spec.Feedback {
	case Negative:
		spec.Weight = -1
	case Rating:
		spec.Neutral, spec.Weight = first.Neutral, first.Weight
	}
	spec.Aggregate = c.Aggregate
	if spec.Aggregate == "" {
		spec.Aggregate = Sum
		if spec.Feedback == Rating {
			spec.Aggregate = Last
		}
	}
	halfLife := m.HalfLife
	if h := m.Relations[relation].HalfLife; h > 0 {
		halfLife = h
	}
	switch {
	case c.NoDecay:
		spec.Rate = 0
	case c.HalfLife > 0:
		spec.Rate = Rate(c.HalfLife)
	case spec.Feedback == Rating:
		spec.Rate = 0
	default:
		spec.Rate = Rate(halfLife)
	}
	return spec, nil
}
