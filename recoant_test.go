package recoant_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/breskos/recoant"
)

func TestRef(t *testing.T) {
	r := recoant.U("42")
	if r.String() != "user:42" {
		t.Fatalf("String() = %q", r.String())
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []recoant.Ref{{ID: "1"}, {Kind: "product"}} {
		if err := bad.Validate(); !errors.Is(err, recoant.ErrInvalidRef) {
			t.Errorf("Validate(%v) = %v", bad, err)
		}
	}
}

func TestSignalValidate(t *testing.T) {
	ok := recoant.Signal{
		Subject: recoant.U("u"),
		Object:  recoant.Ref{Kind: "product", ID: "p"},
		Type:    recoant.Click,
		Value:   1,
		Time:    time.Unix(1, 0),
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(s *recoant.Signal){
		"subject": func(s *recoant.Signal) { s.Subject.ID = "" },
		"object":  func(s *recoant.Signal) { s.Object.Kind = "" },
		"type":    func(s *recoant.Signal) { s.Type = "" },
		"nan":     func(s *recoant.Signal) { s.Value = math.NaN() },
		"inf":     func(s *recoant.Signal) { s.Value = math.Inf(1) },
		"time":    func(s *recoant.Signal) { s.Time = time.Time{} },
	}
	for name, mutate := range cases {
		s := ok
		mutate(&s)
		if err := s.Validate(); !errors.Is(err, recoant.ErrInvalidSignal) {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}
}

func TestRequestValidate(t *testing.T) {
	ok := recoant.Request{Subject: recoant.U("u"), Target: "product", K: 10}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(r *recoant.Request){
		"subject": func(r *recoant.Request) { r.Subject = recoant.Ref{} },
		"target":  func(r *recoant.Request) { r.Target = "" },
		"k":       func(r *recoant.Request) { r.K = 0 },
	}
	for name, mutate := range cases {
		r := ok
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, recoant.ErrInvalidRequest) {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}
}

func TestRequestWhereUnfiltered(t *testing.T) {
	r := recoant.Request{Subject: recoant.U("u"), Target: "product", K: 1, Where: []recoant.Condition{{Attribute: "a", Same: true}}}
	if err := r.Validate(); !errors.Is(err, recoant.ErrUnfiltered) {
		t.Fatalf("Validate() = %v", err)
	}
}

func fp(v float64) *float64 { return &v }

func TestConditionValidate(t *testing.T) {
	good := []struct {
		test string
		c    recoant.Condition
	}{
		{"in", recoant.Condition{Attribute: "brand", In: []string{"x"}}},
		{"range", recoant.Condition{Attribute: "price", Min: fp(1), Max: fp(2)}},
		{"distance", recoant.Condition{Attribute: "loc", MaxDistanceKm: 50}},
		{"distance", recoant.Condition{Attribute: "loc", Near: &[2]float64{52.5, 13.4}, MaxDistanceKm: 5}},
		{"within", recoant.Condition{Attribute: "age", Within: []string{"min_age", "max_age"}, Mutual: true}},
		{"same", recoant.Condition{Attribute: "city", Same: true}},
	}
	for _, g := range good {
		if err := g.c.Validate(); err != nil || g.c.Test() != g.test {
			t.Errorf("%s: Validate() = %v, Test() = %q", g.test, err, g.c.Test())
		}
	}
	if (recoant.Condition{Attribute: "a"}).Test() != "" {
		t.Error("Test() of an empty condition")
	}
	bad := []recoant.Condition{
		{In: []string{"x"}},
		{Attribute: "a"},
		{Attribute: "a", In: []string{"x"}, Same: true},
		{Attribute: "a", Min: fp(math.NaN())},
		{Attribute: "a", Min: fp(3), Max: fp(2)},
		{Attribute: "a", MaxDistanceKm: -1},
		{Attribute: "a", Near: &[2]float64{1, 2}},
		{Attribute: "a", Near: &[2]float64{91, 0}, MaxDistanceKm: 1},
		{Attribute: "a", Within: []string{"x"}},
		{Attribute: "a", Same: true, Mutual: true},
	}
	for i, c := range bad {
		if err := c.Validate(); !errors.Is(err, recoant.ErrInvalidRequest) {
			t.Errorf("case %d: Validate() = %v", i, err)
		}
	}
}
