package bpr_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/bpr"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/store"
)

var ctx = context.Background()

func TestBPR(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, err := bpr.New(s, bpr.Config{Relation: testfix.Rel, Factors: 4, Epochs: 300, Seed: 1})
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
	_ = s.Add(testfix.Sig("new", "x", recoant.Purchase, 1))
	if res, _ := m.Recommend(ctx, recoant.Request{Subject: recoant.U("new"), Target: "product", K: 1}); res != nil {
		t.Errorf("subject not in Fit: %v", res)
	}
}

// A subject who interacted with every object has no negatives and is
// skipped without looping forever.
func TestNoNegatives(t *testing.T) {
	s := testfix.Store(t, []recoant.Signal{testfix.Sig("u", "a", recoant.Click, 1), testfix.Sig("u", "b", recoant.Click, 1), testfix.Sig("v", "a", recoant.Click, 1)})
	m, _ := bpr.New(s, bpr.Config{Relation: testfix.Rel, Epochs: 50, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	for i, c := range []bpr.Config{{}, {Relation: testfix.Rel, Factors: -1}, {Relation: testfix.Rel, LearnRate: math.NaN()}} {
		if _, err := bpr.New(s, c); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	m, _ := bpr.New(s, bpr.Config{Relation: testfix.Rel})
	if c := m.Config(); c.Factors != 32 || c.Epochs != 30 || c.LearnRate != 0.05 || c.Reg != 0.01 {
		t.Errorf("defaults %+v", c)
	}
	unknown, _ := bpr.New(s, bpr.Config{Relation: store.Relation{Subject: "user", Object: "movie"}})
	if err := unknown.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}
