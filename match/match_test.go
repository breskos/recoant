package match_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/match"
	"github.com/breskos/recoant/store"
)

var (
	ctx   = context.Background()
	likes = store.Relation{Subject: recoant.User, Object: recoant.User}
)

// fixed is a forward recommender with fixed scores, in this order.
type fixed struct {
	res []recoant.Scored
	err error
	got recoant.Request
}

func (f *fixed) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	f.got = req
	return f.res, f.err
}

// back scores the reverse direction from a map; missing IDs are NaN.
type back map[string]float64

func (b back) Interest(subjects []recoant.Ref, _ recoant.Ref) []float64 {
	out := make([]float64, len(subjects))
	for i, s := range subjects {
		v, ok := b[s.ID]
		if !ok {
			v = math.NaN()
		}
		out[i] = v
	}
	return out
}

func scored(pairs ...any) []recoant.Scored {
	var out []recoant.Scored
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, recoant.Scored{Ref: recoant.U(pairs[i].(string)), Score: pairs[i+1].(float64)})
	}
	return out
}

func sig(from, to string, typ recoant.SignalType) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: typ, Value: 1, Time: testfix.T0}
}

func newStore(t *testing.T, sigs ...recoant.Signal) *store.Store {
	t.Helper()
	s, err := store.New(testfix.NegativeModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	return s
}

func recommend(t *testing.T, r *match.Reciprocal, k int) []recoant.Scored {
	t.Helper()
	res, err := r.Recommend(ctx, recoant.Request{Subject: recoant.U("me"), Target: recoant.User, K: k})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// a likes me a lot but I like a only a bit; b and I like each other.
func TestCombine(t *testing.T) {
	s := newStore(t, sig("me", "x", recoant.Visit))
	fwd := &fixed{res: scored("a", 0.2, "b", 0.6, "c", 0.9, "d", 0.5, "e", 0.0)}
	rev := back{"a": 1, "b": 0.6, "c": 0, "d": 0.5, "e": 0}
	want := map[match.Combine][]string{
		match.Harmonic:  {"b", "d", "a"},
		match.Geometric: {"b", "d", "a"},
		match.Min:       {"b", "d", "a"},
		match.Product:   {"b", "d", "a"},
	}
	for c, ids := range want {
		r, err := match.New(s, fwd, rev, match.Config{Relation: likes, Combine: c})
		if err != nil {
			t.Fatal(err)
		}
		res := recommend(t, r, 5)
		if got := testfix.IDs(res); !slices.Equal(got, ids) {
			t.Errorf("%s: got %v, want %v", c, got, ids)
		}
		if c == match.Harmonic && math.Abs(res[0].Score-0.6) > 1e-9 {
			t.Errorf("harmonic of 0.6 and 0.6 = %v", res[0].Score)
		}
	}
	if fwd.got.K != 200 {
		t.Errorf("default pool %d, want 200", fwd.got.K)
	}
}

// Scores of each combination for a = 0.8, b = 0.2.
func TestCombineValues(t *testing.T) {
	s := newStore(t, sig("me", "x", recoant.Visit))
	want := map[match.Combine]float64{match.Harmonic: 0.32, match.Geometric: 0.4, match.Min: 0.2, match.Product: 0.16}
	for c, v := range want {
		r, _ := match.New(s, &fixed{res: scored("a", 0.8)}, back{"a": 0.2}, match.Config{Relation: likes, Combine: c, Pool: 7})
		if res := recommend(t, r, 1); len(res) != 1 || math.Abs(res[0].Score-v) > 1e-9 {
			t.Errorf("%s: %v, want %v", c, res, v)
		}
	}
}

// Sigmoid calibration, the minimum per direction and unknown reverse
// interests.
func TestCalibrateAndMinimum(t *testing.T) {
	s := newStore(t, sig("me", "x", recoant.Visit))
	fwd := &fixed{res: scored("a", 2.0, "b", -3.0, "c", 1.0)}
	r, err := match.New(s, fwd, back{"a": 2, "b": 3}, match.Config{Relation: likes, Calibrate: match.Sigmoid, MinEach: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	res := recommend(t, r, 5)
	// b's forward sigmoid is 0.047 < 0.1, c has no reverse vector.
	if got := testfix.IDs(res); !slices.Equal(got, []string{"a"}) {
		t.Errorf("got %v, want [a]", got)
	}
	if sg := 1 / (1 + math.Exp(-2)); math.Abs(res[0].Score-sg) > 1e-9 {
		t.Errorf("score %v, want %v", res[0].Score, sg)
	}
}

// Candidates who rejected the subject are left out, unless asked.
func TestRejectedBack(t *testing.T) {
	s := newStore(t, sig("a", "me", "hide"), sig("me", "x", recoant.Visit))
	r, _ := match.New(s, &fixed{res: scored("a", 0.9, "b", 0.5)}, back{"a": 0.9, "b": 0.5}, match.Config{Relation: likes})
	if got := testfix.IDs(recommend(t, r, 5)); !slices.Equal(got, []string{"b"}) {
		t.Errorf("got %v, want [b]", got)
	}
	res, err := r.Recommend(ctx, recoant.Request{Subject: recoant.U("me"), Target: recoant.User, K: 5, IncludeRejected: true})
	if err != nil || len(res) != 2 {
		t.Errorf("include rejected: %v %v", res, err)
	}
}

// Exposure balancing lowers popular candidates: a is liked by four
// people, b by one; without balancing a wins, with it b.
func TestExposureBalancing(t *testing.T) {
	var sigs []recoant.Signal
	for _, u := range []string{"u1", "u2", "u3", "u4"} {
		sigs = append(sigs, sig(u, "a", recoant.Purchase))
	}
	sigs = append(sigs, sig("u1", "b", recoant.Purchase), sig("u2", "c", "hide"))
	s := newStore(t, sigs...)
	fwd := &fixed{res: scored("a", 0.6, "b", 0.5)}
	rev := back{"a": 0.6, "b": 0.5}
	plain, _ := match.New(s, fwd, rev, match.Config{Relation: likes})
	balanced, _ := match.New(s, fwd, rev, match.Config{Relation: likes, ExposureBalancing: 1})
	for _, r := range []*match.Reciprocal{plain, balanced} {
		if err := r.Fit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := testfix.IDs(recommend(t, plain, 2)); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("plain %v", got)
	}
	if got := testfix.IDs(recommend(t, balanced, 2)); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("balanced %v", got)
	}
	if err := balanced.Refresh(ctx); err != nil {
		t.Error(err)
	}
	if balanced.Config().Combine != match.Harmonic || balanced.Config().Calibrate != match.Clamp {
		t.Errorf("defaults %+v", balanced.Config())
	}
}

func TestFitErrors(t *testing.T) {
	s := newStore(t, sig("me", "x", recoant.Visit))
	other := store.Relation{Subject: recoant.User, Object: "job"}
	r, _ := match.New(s, &fixed{}, back{}, match.Config{Relation: other, ExposureBalancing: 0.5})
	if err := r.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
	r, _ = match.New(s, &fixed{}, back{}, match.Config{Relation: likes, ExposureBalancing: 0.5})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

func TestRequests(t *testing.T) {
	s := newStore(t, sig("me", "x", recoant.Visit))
	boom := errors.New("boom")
	r, _ := match.New(s, &fixed{err: boom}, back{}, match.Config{Relation: likes})
	if _, err := r.Recommend(ctx, recoant.Request{Subject: recoant.U("me"), Target: recoant.User, K: 3}); !errors.Is(err, boom) {
		t.Errorf("forward error: %v", err)
	}
	pass := &fixed{res: scored("x", 1.0)}
	r2, _ := match.New(s, pass, back{}, match.Config{Relation: likes})
	if res, err := r2.Recommend(ctx, recoant.Request{Subject: recoant.U("me"), Target: "job", K: 3}); err != nil || len(res) != 1 || pass.got.K != 3 {
		t.Errorf("other target passed on: %v %v %+v", res, err, pass.got)
	}
	if _, err := r.Recommend(ctx, recoant.Request{Subject: recoant.U("me"), Target: recoant.User}); err == nil {
		t.Error("invalid request accepted")
	}
	r, _ = match.New(s, &fixed{}, back{}, match.Config{Relation: likes})
	if res := recommend(t, r, 3); len(res) != 0 {
		t.Errorf("no candidates: %v", res)
	}
}

func TestConfig(t *testing.T) {
	s := newStore(t)
	if _, err := match.New(nil, &fixed{}, back{}, match.Config{Relation: likes}); !errors.Is(err, match.ErrConfig) {
		t.Errorf("nil store: %v", err)
	}
	for name, c := range map[string]match.Config{
		"relation":  {},
		"combine":   {Relation: likes, Combine: "mean"},
		"calibrate": {Relation: likes, Calibrate: "rank"},
		"min_each":  {Relation: likes, MinEach: 2},
		"exposure":  {Relation: likes, ExposureBalancing: math.NaN()},
		"pool":      {Relation: likes, Pool: -1},
	} {
		if _, err := match.New(s, &fixed{}, back{}, c); !errors.Is(err, match.ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// End to end with ALS on a small dating graph: two groups that like each
// other inside the group. Everyone in group A also likes the star s, but s
// likes nobody in A — one-way ALS recommends s first, reciprocal matching
// does not.
func TestALS(t *testing.T) {
	var sigs []recoant.Signal
	a := []string{"a1", "a2", "a3", "a4", "a5"}
	for _, u := range a {
		for _, v := range a {
			if u != v && (u != "a1" || v != "a2") {
				sigs = append(sigs, sig(u, v, recoant.Purchase))
			}
		}
		if u != "a1" {
			sigs = append(sigs, sig(u, "s", recoant.Purchase))
		}
	}
	sigs = append(sigs, sig("a1", "a3", recoant.Visit))
	for _, u := range []string{"b1", "b2", "b3"} {
		sigs = append(sigs, sig("s", u, recoant.Purchase), sig(u, "s", recoant.Purchase))
	}
	for i := range sigs {
		sigs[i].Time = testfix.T0.Add(time.Duration(i) * time.Minute)
	}
	s := newStore(t, sigs...)
	m, err := als.New(s, als.Config{Relation: likes, Factors: 4, Seed: 1, Iterations: 15})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	req := recoant.Request{Subject: recoant.U("a1"), Target: recoant.User, K: 4, ExcludeSeen: true}
	oneWay, err := m.Recommend(ctx, req)
	if err != nil || len(oneWay) < 2 || oneWay[0].Ref.ID != "a2" {
		t.Fatalf("one way: %v %v", oneWay, err)
	}
	r, _ := match.New(s, m, m, match.Config{Relation: likes, MinEach: 0.2})
	res, err := r.Recommend(ctx, req)
	if got := testfix.IDs(res); err != nil || !slices.Equal(got, []string{"a2"}) {
		t.Errorf("reciprocal: %v %v, want [a2]", got, err)
	}
}
