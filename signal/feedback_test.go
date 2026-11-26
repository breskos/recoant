package signal_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
)

func TestFeedback(t *testing.T) {
	m := signal.DefaultModel()
	m.Rules["stars"] = signal.Rule{Weight: 2, Feedback: signal.Rating, Scale: []float64{1, 5}, Neutral: 3}
	m.Rules["return"] = signal.Rule{Weight: 8, Feedback: signal.Negative}
	m.Rules["like"] = signal.Rule{Weight: 5, Relation: "likes"}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(0, 0)
	sig := func(typ string, v float64) recoant.Signal {
		return recoant.Signal{Type: recoant.SignalType(typ), Value: v, Time: t0}
	}
	cases := []struct {
		typ          string
		v            float64
		rel, channel string
		stored, w    float64
	}{
		{"purchase", 1, "", "positive", 10, 10},
		{"stars", 1, "", "rating", 1, -4},
		{"stars", 9, "", "rating", 5, 4}, // clipped
		{"return", 1, "", "negative", 8, -8},
		{"like", 1, "likes", "positive", 5, 5},
	}
	for _, c := range cases {
		rel, ch, v, ok := m.Route(sig(c.typ, c.v))
		if !ok || rel != c.rel || ch != c.channel || v != c.stored {
			t.Errorf("Route(%s %v) = %q %q %v %v", c.typ, c.v, rel, ch, v, ok)
		}
		if w, _ := m.Weight(sig(c.typ, c.v)); w != c.w {
			t.Errorf("Weight(%s %v) = %v, want %v", c.typ, c.v, w, c.w)
		}
	}
	if _, _, _, ok := m.Route(sig("unknown", 1)); ok {
		t.Error("unknown type routed")
	}
	// Ratings do not decay by default, implicit signals do.
	later := t0.Add(m.HalfLife)
	if got := m.Strength(sig("stars", 5), later); got != 4 {
		t.Errorf("rating after a half-life = %v", got)
	}
	if got := m.Strength(sig("purchase", 1), later); got != 5 {
		t.Errorf("purchase after a half-life = %v", got)
	}
	if got := m.ChannelNames(""); !slices.Equal(got, []string{"negative", "positive", "rating"}) {
		t.Errorf("ChannelNames = %v", got)
	}
	spec, ok := m.ChannelSpec("", "rating")
	if !ok || spec.Aggregate != signal.Last || spec.Rate != 0 || spec.Neutral != 3 || spec.Weight != 2 {
		t.Errorf("rating spec %+v", spec)
	}
	if spec, _ := m.ChannelSpec("", "negative"); spec.Weight != -1 || spec.Aggregate != signal.Sum || spec.Rate == 0 {
		t.Errorf("negative spec %+v", spec)
	}
	if _, ok := m.ChannelSpec("", "nope"); ok {
		t.Error("unused channel resolved")
	}
	m.Channels = map[string]signal.Channel{"rating": {HalfLife: time.Hour}, "negative": {NoDecay: true}}
	if spec, _ := m.ChannelSpec("", "rating"); spec.Rate == 0 {
		t.Error("rating with half-life must decay")
	}
	if spec, _ := m.ChannelSpec("", "negative"); spec.Rate != 0 {
		t.Error("no_decay ignored")
	}
	// A relation can override half-life and channels.
	m.Rules["like"] = signal.Rule{Weight: 5, Relation: "likes"}
	m.Rules["pass"] = signal.Rule{Weight: 3, Relation: "likes", Feedback: signal.Negative}
	m.Relations = map[string]signal.RelationSpec{"likes": {
		Subject: recoant.User, Object: recoant.User, HalfLife: time.Hour,
		Channels: map[string]signal.Channel{"negative": {NoDecay: true}, "positive": {Aggregate: signal.Max}},
	}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := m.RelationNames(); !slices.Equal(got, []string{"", "likes"}) {
		t.Errorf("RelationNames = %v", got)
	}
	if spec, _ := m.ChannelSpec("likes", "positive"); spec.Aggregate != signal.Max || spec.Rate != signal.Rate(time.Hour) {
		t.Errorf("likes/positive %+v", spec)
	}
	if spec, _ := m.ChannelSpec("likes", "negative"); spec.Rate != 0 || spec.Weight != -1 {
		t.Errorf("likes/negative %+v", spec)
	}
	if _, ok := m.ChannelSpec("likes", "rating"); ok {
		t.Error("likes has no rating channel")
	}
	if got := m.Strength(sig("like", 1), t0.Add(time.Hour)); got != 2.5 {
		t.Errorf("like after the relation's half-life = %v", got)
	}
	if r := (signal.Rule{}); r.FeedbackKind() != signal.Implicit || r.ChannelName() != "positive" {
		t.Error("rule defaults")
	}
}

func TestFeedbackValidate(t *testing.T) {
	rules := func(rs ...signal.Rule) map[recoant.SignalType]signal.Rule {
		out := map[recoant.SignalType]signal.Rule{}
		for i, r := range rs {
			out[recoant.SignalType(rune('a'+i))] = r
		}
		return out
	}
	bad := []signal.Model{
		{Rules: rules(signal.Rule{Weight: 1, Feedback: "maybe"})},
		{Rules: rules(signal.Rule{Weight: -1, Feedback: signal.Negative})},
		{Rules: rules(signal.Rule{Weight: 1, Feedback: signal.Rating, Scale: []float64{5, 1}})},
		{Rules: rules(signal.Rule{Weight: 1, Feedback: signal.Rating, Scale: []float64{1}})},
		{Rules: rules(signal.Rule{Weight: 1, Feedback: signal.Rating, Scale: []float64{1, 5}, Neutral: 7})},
		{Rules: rules(signal.Rule{Weight: 1, Channel: "x"}, signal.Rule{Weight: 1, Channel: "x", Feedback: signal.Negative})},
		{Rules: rules(signal.Rule{Weight: 1, Feedback: signal.Rating, Neutral: 3}, signal.Rule{Weight: 1, Feedback: signal.Rating, Neutral: 0})},
		{Channels: map[string]signal.Channel{"": {}}},
		{Channels: map[string]signal.Channel{"x": {Aggregate: "median"}}},
		{Channels: map[string]signal.Channel{"x": {HalfLife: -1}}},
		{Channels: map[string]signal.Channel{"x": {Aggregate: signal.Mean}}},
		{Relations: map[string]signal.RelationSpec{"likes": {Subject: recoant.User}}},
		{Relations: map[string]signal.RelationSpec{"likes": {Subject: recoant.User, Object: recoant.User, HalfLife: -1}}},
		{Relations: map[string]signal.RelationSpec{"likes": {Subject: recoant.User, Object: recoant.User, Retention: -1}}},
		{Relations: map[string]signal.RelationSpec{"likes": {Subject: recoant.User, Object: recoant.User, Channels: map[string]signal.Channel{"x": {Aggregate: "nope"}}}}},
		{Default: &signal.Rule{Weight: 1, Feedback: signal.Rating}, Rules: rules(signal.Rule{Weight: 2, Feedback: signal.Rating})},
	}
	for i, m := range bad {
		if err := m.Validate(); !errors.Is(err, signal.ErrInvalidModel) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}
