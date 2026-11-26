package mf_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/mf"
	"github.com/breskos/recoant/evaluation"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

var ctx = context.Background()

func rating(user, product string, v float64) recoant.Signal {
	return testfix.Sig(user, product, "rating", v)
}

// ratings: users r0–r9 love products g0–g3 (4–5 stars) and dislike h0–h3
// (1–2 stars); users s0–s9 the opposite. Every user misses one product of
// each group, returned as held-out ratings.
func ratings() (train, test []recoant.Signal) {
	for g, users := range []string{"r", "s"} {
		for u := range 10 {
			user := fmt.Sprint(users, u)
			for k := range 4 {
				for h, group := range []string{"g", "h"} {
					v := 1.0 + float64((u+k)%2)
					if h == g {
						v = 4 + float64((u+k)%2)
					}
					sig := rating(user, fmt.Sprint(group, k), v)
					if k == u%4 {
						test = append(test, sig)
					} else {
						train = append(train, sig)
					}
				}
			}
		}
	}
	return train, test
}

func ratingStore(t *testing.T, sigs []recoant.Signal) *store.Store {
	t.Helper()
	m := signal.Model{Rules: map[recoant.SignalType]signal.Rule{"rating": {Weight: 1, Transform: signal.Transform{Kind: signal.Linear}}}}
	s, err := store.New(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVariants(t *testing.T) {
	train, test := ratings()
	var mean float64
	for _, s := range train {
		mean += s.Value
	}
	mean /= float64(len(train))
	baseline := evaluation.Errors(constant(mean), test, nil)
	for _, v := range []mf.Variant{mf.Funk, mf.Biased, mf.SVDPlusPlus} {
		t.Run(string(v), func(t *testing.T) {
			s := ratingStore(t, train)
			m, err := mf.New(s, mf.Config{Relation: testfix.Rel, Variant: v, Factors: 4, Epochs: 200, LearnRate: 0.02, Min: 1, Max: 5, Seed: 1})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Predict(recoant.U("r0"), testfix.P("g0")); ok {
				t.Error("Predict before Fit")
			}
			if err := m.Fit(ctx); err != nil {
				t.Fatal(err)
			}
			rep := evaluation.Errors(m, test, []recoant.SignalType{"rating"})
			if rep.N != len(test) || rep.RMSE > baseline.RMSE/2 {
				t.Errorf("RMSE %.3f, baseline %.3f (n %d)", rep.RMSE, baseline.RMSE, rep.N)
			}
			// r0 misses g0 and h0: g0 must come first.
			res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("r0"), Target: "product", K: 2, ExcludeSeen: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := testfix.IDs(res); len(got) != 2 || got[0] != "g0" {
				t.Errorf("r0: %v", got)
			}
			res, _ = m.Recommend(ctx, recoant.Request{Subject: testfix.P("g1"), Target: "product", K: 1})
			if got := testfix.IDs(res); len(got) != 1 || got[0][0] != 'g' {
				t.Errorf("similar to g1: %v", got)
			}
			res, _ = m.Recommend(ctx, recoant.Request{Subject: recoant.U("r1"), Target: recoant.User, K: 1})
			if got := testfix.IDs(res); len(got) != 1 || got[0][0] != 'r' {
				t.Errorf("similar to r1: %v", got)
			}
			// Unknown subject and object fall back to mean and biases.
			if p, ok := m.Predict(recoant.U("nobody"), testfix.P("nothing")); !ok || p < 1 || p > 5 {
				t.Errorf("fallback %v %v", p, ok)
			}
		})
	}
}

// SVD++ recommends for a subject that was not part of training. Its
// implicit part learns from what subjects rate, so here users mostly rate
// their own group: r users rate g0–g3 with 5 and h0 with 1, s users the
// other way round. (No other pattern in the values, which SVD++ would
// learn as well.)
func TestSVDPlusPlusFoldIn(t *testing.T) {
	var train []recoant.Signal
	for u := range 10 {
		for k := range 4 {
			train = append(train, rating(fmt.Sprint("r", u), fmt.Sprint("g", k), 5))
			train = append(train, rating(fmt.Sprint("s", u), fmt.Sprint("h", k), 5))
		}
		train = append(train, rating(fmt.Sprint("r", u), "h0", 1), rating(fmt.Sprint("s", u), "g0", 1))
	}
	s := ratingStore(t, train)
	pp, _ := mf.New(s, mf.Config{Relation: testfix.Rel, Variant: mf.SVDPlusPlus, Factors: 4, Epochs: 200, LearnRate: 0.02, Seed: 1})
	biased, _ := mf.New(s, mf.Config{Relation: testfix.Rel, Epochs: 10})
	for _, m := range []*mf.MF{pp, biased} {
		if err := m.Fit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Add(rating("newbie", "h1", 5))
	_ = s.Add(rating("newbie", "h2", 5))
	res, err := pp.Recommend(ctx, recoant.Request{Subject: recoant.U("newbie"), Target: "product", K: 2, ExcludeSeen: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := testfix.IDs(res); len(got) == 0 || got[0] != "h3" {
		t.Errorf("newbie: %v, want h3 first", got)
	}
	if res, _ := biased.Recommend(ctx, recoant.Request{Subject: recoant.U("newbie"), Target: "product", K: 2}); res != nil {
		t.Errorf("biased MF has no vector for a new subject: %v", res)
	}
	if p, ok := pp.Predict(recoant.U("newbie"), testfix.P("h3")); !ok || p < 3 {
		t.Errorf("Predict(newbie, h3) = %v, %v", p, ok)
	}
}

func TestErrors(t *testing.T) {
	s := ratingStore(t, []recoant.Signal{rating("u", "p", 3)})
	bad := []mf.Config{
		{},
		{Relation: testfix.Rel, Variant: "nmf"},
		{Relation: testfix.Rel, Factors: -1},
		{Relation: testfix.Rel, LearnRate: math.NaN()},
	}
	for i, c := range bad {
		if _, err := mf.New(s, c); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	m, _ := mf.New(s, mf.Config{Relation: testfix.Rel})
	if c := m.Config(); c.Variant != mf.Biased || c.Factors != 32 || c.Epochs != 20 {
		t.Errorf("defaults %+v", c)
	}
	if res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("u"), Target: "product", K: 1}); res != nil || err != nil {
		t.Errorf("before Fit: %v %v", res, err)
	}
	unknown, _ := mf.New(s, mf.Config{Relation: store.Relation{Subject: "user", Object: "movie"}})
	if err := unknown.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

type constant float64

func (c constant) Predict(recoant.Ref, recoant.Ref) (float64, bool) { return float64(c), true }
