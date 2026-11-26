package als_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/store"
)

var ctx = context.Background()

func TestALS(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, err := als.New(s, als.Config{Relation: testfix.Rel, Factors: 4, Seed: 1, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 1}); res != nil || err != nil {
		t.Fatalf("before Fit: %v %v", res, err)
	}
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	testfix.CheckGroups(t, m, true)
	testfix.CheckInterest(t, m)
	testfix.CheckRoles(t, m, recoant.U("u1"), testfix.P("a"))
}

// New signals change the recommendations at once (fold-in) and Refresh
// adds vectors for new objects.
func TestFoldInAndRefresh(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, _ := als.New(s, als.Config{Relation: testfix.Rel, Factors: 4, Seed: 1})
	if err := m.Refresh(ctx); err != nil { // first Refresh fits
		t.Fatal(err)
	}
	if err := m.Refresh(ctx); err != nil { // nothing changed
		t.Fatal(err)
	}
	_ = s.Add(testfix.Sig("new", "x", recoant.Purchase, 1))
	res, _ := m.Recommend(ctx, recoant.Request{Subject: recoant.U("new"), Target: "product", K: 1, ExcludeSeen: true})
	if got := testfix.IDs(res); len(got) != 1 || (got[0] != "y" && got[0] != "z") {
		t.Errorf("new user: %v", got)
	}
	for _, u := range []string{"u4", "u5", "u6"} {
		_ = s.Add(testfix.Sig(u, "fresh", recoant.Purchase, 1))
	}
	if res, _ := m.Recommend(ctx, recoant.Request{Subject: testfix.P("fresh"), Target: "product", K: 1}); res != nil {
		t.Errorf("fresh before Refresh: %v", res)
	}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	res, _ = m.Recommend(ctx, recoant.Request{Subject: testfix.P("fresh"), Target: "product", K: 1})
	if got := testfix.IDs(res); len(got) != 1 || (got[0] != "x" && got[0] != "y" && got[0] != "z") {
		t.Errorf("similar to fresh: %v", got)
	}
}

func TestErrors(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	for i, c := range []als.Config{{}, {Relation: testfix.Rel, Factors: -1}, {Relation: testfix.Rel, Reg: -1}, {Relation: testfix.Rel, Alpha: math.Inf(1)}} {
		if _, err := als.New(s, c); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	m, _ := als.New(s, als.Config{Relation: testfix.Rel})
	if c := m.Config(); c.Factors != 32 || c.Iterations != 10 || c.Reg != 0.1 || c.Alpha != 1 {
		t.Errorf("defaults %+v", c)
	}
	unknown, _ := als.New(s, als.Config{Relation: store.Relation{Subject: "user", Object: "movie"}})
	if err := unknown.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Fit: %v", err)
	}
	_ = m.Fit(ctx)
	_ = s.Add(testfix.Sig("u1", "c", recoant.Click, 1))
	if err := m.Refresh(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Refresh: %v", err)
	}
	_ = s.Add(recoant.Signal{Subject: recoant.U("u1"), Object: recoant.Ref{Kind: "movie", ID: "m"}, Type: recoant.Click, Value: 1, Time: testfix.T0})
	if err := unknown.Refresh(ctx); err != nil {
		t.Errorf("Refresh fits: %v", err)
	}
}

func TestRejected(t *testing.T) {
	m, _ := als.New(testfix.NegativeStore(t), als.Config{Relation: testfix.Rel, Factors: 4, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	testfix.CheckRejected(t, m)
}

// Cancellation at every check of Fit and Refresh returns context.Canceled.
func TestCancelEverywhere(t *testing.T) {
	testfix.CheckCancel(t, func(c context.Context) error {
		m, _ := als.New(testfix.Store(t, testfix.Groups), als.Config{Relation: testfix.Rel, Factors: 2, Iterations: 2, Seed: 1, Workers: 1})
		return m.Fit(c)
	})
	testfix.CheckCancel(t, func(c context.Context) error {
		s := testfix.Store(t, testfix.Groups)
		m, _ := als.New(s, als.Config{Relation: testfix.Rel, Factors: 2, Iterations: 2, Seed: 1, Workers: 1})
		if err := m.Fit(ctx); err != nil {
			t.Fatal(err)
		}
		_ = s.Add(testfix.Sig("u1", "c", recoant.Click, 1))
		return m.Refresh(c)
	})
}

// A history with objects newer than the model folds in without them, and a
// subject without history in the relation gets nothing.
func TestFoldInEdgeCases(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, _ := als.New(s, als.Config{Relation: testfix.Rel, Factors: 4, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Add(testfix.Sig("u1", "brand-new", recoant.Purchase, 1))
	if res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 2, ExcludeSeen: true}); err != nil || len(res) == 0 {
		t.Errorf("fold-in with a new object: %v %v", res, err)
	}
	_ = s.Add(recoant.Signal{Subject: recoant.U("cinephile"), Object: recoant.Ref{Kind: "movie", ID: "m"}, Type: recoant.Click, Value: 1, Time: testfix.T0})
	if res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("cinephile"), Target: "product", K: 2}); err != nil || res != nil {
		t.Errorf("subject without history: %v %v", res, err)
	}
}

// Collective factorization: n bought group A products but liked nobody
// yet. With the purchases shared, ALS on likes recommends group A people.
func TestShared(t *testing.T) {
	sigs := append([]recoant.Signal(nil), testfix.Groups...)
	for _, g := range [][]string{{"u1", "u2", "u3"}, {"u4", "u5", "u6"}} {
		for _, a := range g {
			for _, b := range g {
				if a != b {
					sigs = append(sigs, recoant.Signal{Subject: recoant.U(a), Object: recoant.U(b), Type: recoant.Purchase, Value: 1, Time: testfix.T0})
				}
			}
		}
	}
	sigs = append(sigs, testfix.Sig("n", "a", recoant.Purchase, 1), testfix.Sig("n", "b", recoant.Purchase, 1))
	s := testfix.Store(t, sigs)
	people := store.Relation{Subject: recoant.User, Object: recoant.User}
	jobs := store.Relation{Subject: recoant.User, Object: "job"} // no signals yet
	req := recoant.Request{Subject: recoant.U("n"), Target: recoant.User, K: 2, ExcludeSeen: true}
	plain, _ := als.New(s, als.Config{Relation: people, Factors: 4, Seed: 1})
	if err := plain.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := plain.Recommend(ctx, req); len(res) != 0 {
		t.Errorf("without shared: %v", res)
	}
	m, err := als.New(s, als.Config{Relation: people, Shared: []store.Relation{testfix.Rel, jobs}, SharedWeight: 2, Factors: 4, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, fit := range []func(context.Context) error{m.Fit, m.Refresh} {
		if err := fit(ctx); err != nil {
			t.Fatal(err)
		}
		res, err := m.Recommend(ctx, req)
		if err != nil || len(res) != 2 {
			t.Fatalf("with shared: %v %v", res, err)
		}
		for _, x := range res {
			if x.Ref.ID > "u3" {
				t.Errorf("with shared: %v, want group A", testfix.IDs(res))
			}
		}
	}
	if in := m.Interest([]recoant.Ref{recoant.U("u2"), recoant.U("u5")}, recoant.U("u1")); !(in[0] > in[1]) {
		t.Errorf("interest in u1: %v", in)
	}
	for name, cfg := range map[string]als.Config{
		"own relation": {Relation: people, Shared: []store.Relation{people}},
		"other kind":   {Relation: people, Shared: []store.Relation{{Subject: "product", Object: recoant.User}}},
		"weight":       {Relation: people, Shared: []store.Relation{testfix.Rel}, SharedWeight: -1},
	} {
		if _, err := als.New(s, cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
