package signal_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTransformApply(t *testing.T) {
	cases := []struct {
		tr   signal.Transform
		v, w float64
	}{
		{signal.Transform{}, 7, 1},
		{signal.Transform{Kind: signal.Constant}, -3, 1},
		{signal.Transform{Kind: signal.Linear}, -3, -3},
		{signal.Transform{Kind: signal.Log}, math.E - 1, 1},
		{signal.Transform{Kind: signal.Log}, -5, 0},
		{signal.Transform{Kind: signal.Saturate, Param: 60}, 60, 1 - math.Exp(-1)},
		{signal.Transform{Kind: signal.Threshold, Param: 5}, 4.9, 0},
		{signal.Transform{Kind: signal.Threshold, Param: 5}, 5, 1},
		{signal.Transform{Kind: signal.Cap, Param: 10}, 25, 10},
		{signal.Transform{Kind: signal.Cap, Param: 10}, 3, 3},
	}
	for _, c := range cases {
		if got := c.tr.Apply(c.v); !near(got, c.w) {
			t.Errorf("%+v.Apply(%v) = %v, want %v", c.tr, c.v, got, c.w)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := signal.DefaultModel().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []signal.Model{
		{Rules: map[recoant.SignalType]signal.Rule{"": {Weight: 1}}},
		{Rules: map[recoant.SignalType]signal.Rule{"x": {Weight: math.NaN()}}},
		{Rules: map[recoant.SignalType]signal.Rule{"x": {Weight: 1, Transform: signal.Transform{Kind: "nope"}}}},
		{Rules: map[recoant.SignalType]signal.Rule{"x": {Weight: 1, Transform: signal.Transform{Kind: signal.Saturate}}}},
		{Rules: map[recoant.SignalType]signal.Rule{"x": {Weight: 1, Transform: signal.Transform{Kind: signal.Cap, Param: math.Inf(1)}}}},
		{Rules: map[recoant.SignalType]signal.Rule{"x": {Weight: 1, Transform: signal.Transform{Kind: signal.Threshold, Param: math.NaN()}}}},
		{Default: &signal.Rule{Weight: math.Inf(-1)}},
		{HalfLife: -time.Second},
	}
	for i, m := range bad {
		if err := m.Validate(); !errors.Is(err, signal.ErrInvalidModel) {
			t.Errorf("case %d: Validate() = %v", i, err)
		}
	}
	ok := signal.Model{Rules: map[recoant.SignalType]signal.Rule{"x": {Weight: 1, Transform: signal.Transform{Kind: signal.Threshold, Param: 2}}}, Default: &signal.Rule{Weight: 0.5}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWeightAndStrength(t *testing.T) {
	m := signal.DefaultModel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := recoant.Signal{Type: recoant.Purchase, Value: 1, Time: t0}
	if w, ok := m.Weight(s); !ok || w != 10 {
		t.Fatalf("Weight = %v, %v", w, ok)
	}
	if got := m.Strength(s, t0.Add(m.HalfLife)); !near(got, 5) {
		t.Errorf("after one half-life: %v", got)
	}
	if got := m.Strength(s, t0.Add(-time.Hour)); got != 10 {
		t.Errorf("future signal: %v", got)
	}
	unknown := recoant.Signal{Type: "share", Value: 1, Time: t0}
	if _, ok := m.Weight(unknown); ok {
		t.Error("unknown type should be ignored")
	}
	if got := m.Strength(unknown, t0); got != 0 {
		t.Errorf("Strength(unknown) = %v", got)
	}
	m.Default = &signal.Rule{Weight: 0.5}
	if w, ok := m.Weight(unknown); !ok || w != 0.5 {
		t.Errorf("default rule: %v, %v", w, ok)
	}
	m.HalfLife = 0
	if m.Rate() != 0 || m.Decay(time.Hour) != 1 {
		t.Error("zero half-life must disable decay")
	}
}
