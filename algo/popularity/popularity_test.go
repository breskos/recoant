package popularity_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func p(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

func sig(user, product string, typ recoant.SignalType, at time.Duration) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(user), Object: p(product), Type: typ, Value: 1, Time: t0.Add(at)}
}

func ids(res []recoant.Scored) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Ref.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var history = []recoant.Signal{
	sig("u1", "a", recoant.Purchase, 0), // a: 10, old
	sig("u2", "b", recoant.Click, 48*time.Hour),
	sig("u3", "b", recoant.Click, 48*time.Hour),
	sig("u3", "c", recoant.Visit, 48*time.Hour),
	sig("u3", "c", "share", 48*time.Hour), // ignored
}

func TestAllTimeAndTrending(t *testing.T) {
	ctx := context.Background()
	all, _ := popularity.New(signal.DefaultModel(), popularity.Config{})
	trend, _ := popularity.New(signal.DefaultModel(), popularity.Config{HalfLife: 6 * time.Hour})
	for _, m := range []*popularity.Popularity{all, trend} {
		if err := m.Fit(ctx, history); err != nil {
			t.Fatal(err)
		}
	}
	req := recoant.Request{Subject: recoant.U("u9"), Target: "product", K: 10}
	res, err := all.Recommend(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res); !equal(got, []string{"a", "b", "c"}) || res[0].Score != 10 {
		t.Errorf("all-time = %v", res)
	}
	res, _ = trend.Recommend(ctx, req)
	if got := ids(res); !equal(got, []string{"b", "c", "a"}) {
		t.Errorf("trending = %v", got)
	}
	if got := trend.Score(p("a"), t0.Add(6*time.Hour)); math.Abs(got-5) > 1e-9 {
		t.Errorf("Score after one half-life = %v", got)
	}
	if trend.Score(p("zzz"), t0) != 0 || trend.Score(recoant.Ref{Kind: "none", ID: "x"}, t0) != 0 {
		t.Error("unknown Score")
	}
}

func TestExclusions(t *testing.T) {
	ctx := context.Background()
	s, _ := store.New(signal.DefaultModel())
	m, _ := popularity.New(signal.DefaultModel(), popularity.Config{})
	for _, x := range history {
		_ = s.Add(x)
		_ = m.Observe(x)
	}
	req := recoant.Request{Subject: recoant.U("u3"), Target: "product", K: 10, ExcludeSeen: true}
	if _, err := m.Recommend(ctx, req); !errors.Is(err, popularity.ErrNoSeen) {
		t.Fatalf("without Seen: %v", err)
	}
	m.SetSeen(s)
	res, _ := m.Recommend(ctx, req)
	if got := ids(res); !equal(got, []string{"a"}) {
		t.Errorf("ExcludeSeen = %v", got)
	}
	req = recoant.Request{
		Subject: p("a"), Target: "product", K: 1,
		Exclude: []recoant.Ref{p("b"), p("unknown"), recoant.U("u1")},
	}
	res, _ = m.Recommend(ctx, req)
	if got := ids(res); !equal(got, []string{"c"}) {
		t.Errorf("Exclude + subject = %v", got)
	}
	req = recoant.Request{Subject: recoant.U("x"), Target: "product", K: 5, Filter: func(r recoant.Ref) bool { return r.ID != "a" }}
	res, _ = m.Recommend(ctx, req)
	if got := ids(res); !equal(got, []string{"b", "c"}) {
		t.Errorf("Filter = %v", got)
	}
	res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("x"), Target: "article", K: 5})
	if err != nil || res != nil {
		t.Errorf("unknown target = %v, %v", res, err)
	}
	if _, err := m.Recommend(ctx, recoant.Request{}); !errors.Is(err, recoant.ErrInvalidRequest) {
		t.Errorf("invalid request: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.Recommend(canceled, recoant.Request{Subject: recoant.U("x"), Target: "product", K: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Recommend: %v", err)
	}
	if err := m.Fit(canceled, history); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Fit: %v", err)
	}
}

func TestErrors(t *testing.T) {
	if _, err := popularity.New(signal.Model{HalfLife: -1}, popularity.Config{}); err == nil {
		t.Error("invalid model")
	}
	if _, err := popularity.New(signal.DefaultModel(), popularity.Config{HalfLife: -1}); err == nil {
		t.Error("invalid half-life")
	}
	m, _ := popularity.New(signal.DefaultModel(), popularity.Config{})
	if err := m.Fit(context.Background(), []recoant.Signal{{}}); !errors.Is(err, recoant.ErrInvalidSignal) {
		t.Errorf("Fit invalid: %v", err)
	}
}

func TestRebase(t *testing.T) {
	m, _ := popularity.New(signal.DefaultModel(), popularity.Config{HalfLife: time.Hour})
	for h := range 500 {
		if err := m.Observe(sig("u", "a", recoant.Visit, time.Duration(h)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.Score(p("a"), t0.Add(499*time.Hour)); math.Abs(got-2) > 1e-6 {
		t.Errorf("Score = %v, want 2", got)
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	m, _ := popularity.New(signal.DefaultModel(), popularity.Config{HalfLife: time.Hour})
	_ = m.Fit(ctx, history)
	r, err := popularity.FromSnapshot(m.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	req := recoant.Request{Subject: recoant.U("x"), Target: "product", K: 3}
	a, _ := m.Recommend(ctx, req)
	b, _ := r.Recommend(ctx, req)
	if len(a) != len(b) {
		t.Fatal("length differs")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("%d: %v != %v", i, a[i], b[i])
		}
	}
	bad := []func(sn *popularity.Snapshot){
		func(sn *popularity.Snapshot) { sn.Version = 0 },
		func(sn *popularity.Snapshot) { sn.Kinds = append(sn.Kinds, sn.Kinds[0]) },
		func(sn *popularity.Snapshot) { sn.Kinds[0].Scores = nil },
		func(sn *popularity.Snapshot) { sn.Kinds[0].IDs[1] = sn.Kinds[0].IDs[0] },
		func(sn *popularity.Snapshot) { sn.Config.HalfLife = -1 },
	}
	for i, mutate := range bad {
		sn := m.Snapshot()
		mutate(sn)
		if _, err := popularity.FromSnapshot(sn); err == nil {
			t.Errorf("case %d: no error", i)
		}
	}
	if _, err := popularity.FromSnapshot(nil); !errors.Is(err, popularity.ErrInvalidSnapshot) {
		t.Error("nil snapshot")
	}
}
