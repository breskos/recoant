package svd_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/svd"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/store"
)

var ctx = context.Background()

func TestPureSVD(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, err := svd.New(s, svd.Config{Relation: testfix.Rel, Factors: 2, Seed: 1, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 3})
	if err != nil || res != nil {
		t.Fatalf("before Fit: %v, %v", res, err)
	}
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	testfix.CheckGroups(t, m, true)
	testfix.CheckInterest(t, m)
	sv := m.SingularValues()
	if len(sv) != 2 || sv[0] < sv[1] || sv[1] <= 0 {
		t.Errorf("singular values %v", sv)
	}
	res, err = m.Recommend(ctx, recoant.Request{Subject: recoant.U("stranger"), Target: "product", K: 3})
	if err != nil || res != nil {
		t.Errorf("unknown user: %v, %v", res, err)
	}
}

// With full rank, PCA reconstructs a training user's row exactly: the
// score of every product is the user's strength for it (0 if unseen). This
// checks centering and fold-in together.
func TestPCAReconstruction(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, _ := svd.New(s, svd.Config{Relation: testfix.Rel, Factors: 6, Center: true, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"u1", "u5"} {
		res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U(user), Target: "product", K: 6})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]float64{}
		for _, r := range res {
			got[r.Ref.ID] = r.Score
		}
		for _, id := range []string{"a", "b", "c", "x", "y", "z"} {
			want := s.Strength(recoant.U(user), testfix.P(id), testfix.T0)
			if math.Abs(got[id]-want) > 1e-4 {
				t.Errorf("%s/%s: score %v, strength %v", user, id, got[id], want)
			}
		}
	}
}

// A user who was not part of training is folded in from the live history.
func TestFoldIn(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	m, _ := svd.New(s, svd.Config{Relation: testfix.Rel, Factors: 2, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Add(testfix.Sig("new", "y", recoant.Purchase, 1))
	res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("new"), Target: "product", K: 2, ExcludeSeen: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Ref.ID != "x" && r.Ref.ID != "z" {
			t.Errorf("new user got %v", testfix.IDs(res))
		}
	}
	// A product added after Fit has no vector yet.
	_ = s.Add(testfix.Sig("u1", "fresh", recoant.Visit, 1))
	res, err = m.Recommend(ctx, recoant.Request{Subject: testfix.P("fresh"), Target: "product", K: 2})
	if err != nil || res != nil {
		t.Errorf("new product: %v, %v", res, err)
	}
}

func TestErrors(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	if _, err := svd.New(s, svd.Config{}); err == nil {
		t.Error("empty relation accepted")
	}
	if _, err := svd.New(s, svd.Config{Relation: testfix.Rel, Factors: -1}); err == nil {
		t.Error("negative factors accepted")
	}
	m, _ := svd.New(s, svd.Config{Relation: testfix.Rel})
	if m.Config().Factors != 64 || m.Config().Oversample != 10 || m.Config().PowerIters != 2 {
		t.Errorf("defaults %+v", m.Config())
	}
	bad, _ := svd.New(s, svd.Config{Relation: store.Relation{Subject: "user", Object: "article"}})
	if err := bad.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Fit: %v", err)
	}
	if _, err := m.Recommend(canceled, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Recommend: %v", err)
	}
	if _, err := m.Recommend(ctx, recoant.Request{}); !errors.Is(err, recoant.ErrInvalidRequest) {
		t.Errorf("invalid request: %v", err)
	}
	if _, err := m.Recommend(ctx, recoant.Request{Subject: testfix.P("a"), Target: recoant.User, K: 1}); !errors.Is(err, recoant.ErrUnsupported) {
		t.Errorf("unsupported: %v", err)
	}
}

func TestRejected(t *testing.T) {
	m, _ := svd.New(testfix.NegativeStore(t), svd.Config{Relation: testfix.Rel, Factors: 2, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	testfix.CheckRejected(t, m)
}

// A subject without history in the relation gets nothing.
func TestNoHistory(t *testing.T) {
	s := testfix.Store(t, testfix.Groups)
	_ = s.Add(recoant.Signal{Subject: recoant.U("cinephile"), Object: recoant.Ref{Kind: "movie", ID: "m"}, Type: recoant.Click, Value: 1, Time: testfix.T0})
	m, _ := svd.New(s, svd.Config{Relation: testfix.Rel, Factors: 2, Seed: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("cinephile"), Target: "product", K: 2}); err != nil || res != nil {
		t.Errorf("subject without history: %v %v", res, err)
	}
}
