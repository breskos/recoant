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
