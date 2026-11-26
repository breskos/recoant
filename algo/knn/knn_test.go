package knn_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/knn"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

var (
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rel = store.Relation{Subject: recoant.User, Object: "product"}
	ctx = context.Background()
)

func p(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

func sig(user, product string, typ recoant.SignalType, value float64) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(user), Object: p(product), Type: typ, Value: value, Time: t0}
}

// Two groups: users u1–u3 like a, b, c; users u4–u6 like x, y, z.
var history = []recoant.Signal{
	sig("u1", "a", recoant.Purchase, 1), sig("u1", "b", recoant.Click, 1),
	sig("u2", "a", recoant.Click, 1), sig("u2", "b", recoant.Purchase, 1), sig("u2", "c", recoant.Click, 1),
	sig("u3", "b", recoant.Click, 1), sig("u3", "c", recoant.Purchase, 1),
	sig("u4", "x", recoant.Purchase, 1), sig("u4", "y", recoant.Click, 1),
	sig("u5", "x", recoant.Click, 1), sig("u5", "y", recoant.Click, 1), sig("u5", "z", recoant.Purchase, 1),
	sig("u6", "y", recoant.Click, 1), sig("u6", "z", recoant.Click, 1),
}

func newStore(t *testing.T, sigs []recoant.Signal) *store.Store {
	t.Helper()
	s, err := store.New(signal.DefaultModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	return s
}

func ids(res []recoant.Scored) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Ref.ID
	}
	return out
}

func TestConfig(t *testing.T) {
	s := newStore(t, history)
	bad := []knn.Config{
		{},
		{Relation: rel, Similarity: "pearson"},
		{Relation: rel, Neighbors: -1},
		{Relation: rel, Shrink: -1},
		{Relation: rel, Workers: -2},
	}
	for i, c := range bad {
		if _, err := knn.NewItemKNN(s, c); err == nil {
			t.Errorf("ItemKNN case %d accepted", i)
		}
		if _, err := knn.NewUserKNN(s, c); err == nil {
			t.Errorf("UserKNN case %d accepted", i)
		}
	}
	m, _ := knn.NewItemKNN(s, knn.Config{Relation: rel})
	if c := m.Config(); c.Neighbors != 50 || c.Similarity != knn.Cosine {
		t.Errorf("defaults = %+v", c)
	}
	u, _ := knn.NewUserKNN(s, knn.Config{Relation: rel})
	if u.Config().Neighbors != 50 {
		t.Error("UserKNN defaults")
	}
	unknown := store.Relation{Subject: recoant.User, Object: "article"}
	m, _ = knn.NewItemKNN(s, knn.Config{Relation: unknown})
	if err := m.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("ItemKNN unknown relation: %v", err)
	}
	if err := m.Refresh(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("ItemKNN Refresh unknown relation: %v", err)
	}
	u, _ = knn.NewUserKNN(s, knn.Config{Relation: unknown})
	if err := u.Fit(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("UserKNN unknown relation: %v", err)
	}
	if err := u.Refresh(ctx); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("UserKNN Refresh unknown relation: %v", err)
	}
}

func TestItemKNN(t *testing.T) {
	for _, sim := range []knn.Similarity{knn.Cosine, knn.Jaccard, knn.AdjustedCosine} {
		t.Run(string(sim), func(t *testing.T) {
			s := newStore(t, history)
			m, _ := knn.NewItemKNN(s, knn.Config{Relation: rel, Similarity: sim, Workers: 2})
			if err := m.Fit(ctx); err != nil {
				t.Fatal(err)
			}
			for _, group := range [][]string{{"a", "b", "c"}, {"x", "y", "z"}} {
				for _, id := range group {
					for _, n := range m.Neighbors(p(id)) {
						if !slices.Contains(group, n.Ref.ID) {
							t.Errorf("%s has neighbor %s from the other group", id, n.Ref.ID)
						}
					}
				}
			}
		})
	}
}

func TestItemKNNRecommend(t *testing.T) {
	s := newStore(t, history)
	m, _ := knn.NewItemKNN(s, knn.Config{Relation: rel})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ids(m.Neighbors(p("a"))); len(got) == 0 || got[0] != "b" {
		t.Errorf("Neighbors(a) = %v", got)
	}
	if m.Neighbors(recoant.U("u1")) != nil || m.Neighbors(p("nope")) != nil {
		t.Error("Neighbors of wrong kind or unknown")
	}
	req := recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 5, ExcludeSeen: true}
	res, err := m.Recommend(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res); !slices.Equal(got, []string{"c"}) {
		t.Errorf("u1 = %v, want [c]", got)
	}
	req.ExcludeSeen = false
	res, _ = m.Recommend(ctx, req)
	if got := ids(res); len(got) != 3 || slices.Contains(got, "x") {
		t.Errorf("u1 with seen = %v", got)
	}
	req.Exclude = []recoant.Ref{p("a")}
	req.Filter = func(r recoant.Ref) bool { return r.ID != "c" }
	res, _ = m.Recommend(ctx, req)
	if got := ids(res); !slices.Equal(got, []string{"b"}) {
		t.Errorf("exclude + filter = %v", got)
	}
	res, _ = m.Recommend(ctx, recoant.Request{Subject: p("y"), Target: "product", K: 1})
	if got := ids(res); len(got) != 1 || (got[0] != "x" && got[0] != "z") {
		t.Errorf("similar to y = %v", got)
	}
	res, err = m.Recommend(ctx, recoant.Request{Subject: recoant.U("stranger"), Target: "product", K: 1})
	if res != nil || err != nil {
		t.Errorf("unknown subject = %v, %v", res, err)
	}
	for _, r := range []recoant.Request{
		{Subject: recoant.U("u1"), Target: recoant.User, K: 1},
		{Subject: recoant.Ref{Kind: "article", ID: "a"}, Target: "product", K: 1},
	} {
		if _, err := m.Recommend(ctx, r); !errors.Is(err, knn.ErrUnsupported) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	if _, err := m.Recommend(ctx, recoant.Request{}); !errors.Is(err, recoant.ErrInvalidRequest) {
		t.Errorf("invalid request: %v", err)
	}
}

func TestUserKNN(t *testing.T) {
	s := newStore(t, history)
	m, _ := knn.NewUserKNN(s, knn.Config{Relation: rel, Similarity: knn.Jaccard})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	similar := ids(m.Neighbors(recoant.U("u1")))
	if len(similar) != 2 || slices.Contains(similar, "u4") {
		t.Errorf("Neighbors(u1) = %v", similar)
	}
	if m.Neighbors(p("a")) != nil || m.Neighbors(recoant.U("nope")) != nil {
		t.Error("Neighbors of wrong kind or unknown")
	}
	res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: recoant.User, K: 5})
	if err != nil || len(res) != 2 || res[0].Ref.Kind != recoant.User {
		t.Errorf("user-to-user = %v, %v", res, err)
	}
	res, _ = m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 5, ExcludeSeen: true})
	if got := ids(res); !slices.Equal(got, []string{"c"}) {
		t.Errorf("u1 = %v, want [c]", got)
	}
	res, err = m.Recommend(ctx, recoant.Request{Subject: recoant.U("stranger"), Target: "product", K: 1})
	if res != nil || err != nil {
		t.Errorf("unknown subject = %v, %v", res, err)
	}
	if _, err := m.Recommend(ctx, recoant.Request{Subject: p("a"), Target: "product", K: 1}); !errors.Is(err, knn.ErrUnsupported) {
		t.Errorf("object subject: %v", err)
	}
	if _, err := m.Recommend(ctx, recoant.Request{}); !errors.Is(err, recoant.ErrInvalidRequest) {
		t.Errorf("invalid request: %v", err)
	}
	adj, _ := knn.NewUserKNN(s, knn.Config{Relation: rel, Similarity: knn.AdjustedCosine})
	if err := adj.Fit(ctx); err != nil {
		t.Fatal(err)
	}
}

// Explicit ratings: AdjustedCosine sees that r1 and r2 rate alike relative
// to their own means, although r2 rates everything higher.
func TestAdjustedCosine(t *testing.T) {
	rating := func(user, product string, v float64) recoant.Signal {
		return sig(user, product, "rating", v)
	}
	m := signal.Model{Rules: map[recoant.SignalType]signal.Rule{"rating": {Weight: 1, Transform: signal.Transform{Kind: signal.Linear}}}}
	s, _ := store.New(m)
	_, _ = s.AddBatch(ctx, []recoant.Signal{
		rating("r1", "a", 1), rating("r1", "b", 3), rating("r1", "c", 5),
		rating("r2", "a", 3), rating("r2", "b", 4), rating("r2", "c", 5),
		rating("r3", "a", 5), rating("r3", "b", 3), rating("r3", "c", 1),
	})
	u, _ := knn.NewUserKNN(s, knn.Config{Relation: rel, Similarity: knn.AdjustedCosine})
	if err := u.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ids(u.Neighbors(recoant.U("r1"))); !slices.Equal(got, []string{"r2"}) {
		t.Errorf("Neighbors(r1) = %v, want [r2]", got)
	}
}

func TestMaxDegree(t *testing.T) {
	// A power user who touched everything links the two groups.
	sigs := slices.Clone(history)
	for _, id := range []string{"a", "b", "c", "x", "y", "z"} {
		sigs = append(sigs, sig("power", id, recoant.Purchase, 1))
	}
	s := newStore(t, sigs)
	m, _ := knn.NewItemKNN(s, knn.Config{Relation: rel, MaxDegree: 3})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, n := range m.Neighbors(p("a")) {
		if n.Ref.ID == "x" || n.Ref.ID == "y" || n.Ref.ID == "z" {
			t.Errorf("power user not ignored: a ~ %s", n.Ref.ID)
		}
	}
}

func TestCanceled(t *testing.T) {
	s := newStore(t, history)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	m, _ := knn.NewItemKNN(s, knn.Config{Relation: rel})
	if err := m.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("ItemKNN: %v", err)
	}
	u, _ := knn.NewUserKNN(s, knn.Config{Relation: rel})
	if err := u.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("UserKNN: %v", err)
	}
	_ = m.Fit(ctx)
	_ = u.Fit(ctx)
	_ = s.Add(sig("u1", "c", recoant.Click, 1))
	if err := m.Refresh(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("ItemKNN Refresh: %v", err)
	}
	if err := u.Refresh(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("UserKNN Refresh: %v", err)
	}
	if _, err := m.Recommend(canceled, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("ItemKNN Recommend: %v", err)
	}
	if _, err := u.Recommend(canceled, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("UserKNN Recommend: %v", err)
	}
}

type neighborer interface {
	Fit(context.Context) error
	Refresh(context.Context) error
	Neighbors(recoant.Ref) []recoant.Scored
}

// After new signals, Refresh must give the same neighbors as a full Fit
// as long as no list is truncated (Neighbors larger than any row).
func TestRefreshMatchesFit(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	random := func(n int) []recoant.Signal {
		out := make([]recoant.Signal, n)
		for i := range out {
			out[i] = sig(fmt.Sprint("u", rng.IntN(40)), fmt.Sprint("p", rng.IntN(30)), recoant.Click, 1)
		}
		return out
	}
	first, later := random(300), random(60)
	for _, sim := range []knn.Similarity{knn.Cosine, knn.Jaccard} {
		for _, kind := range []string{"item", "user"} {
			t.Run(string(sim)+"/"+kind, func(t *testing.T) {
				build := func(s *store.Store) neighborer {
					cfg := knn.Config{Relation: rel, Similarity: sim, Neighbors: 1000}
					if kind == "item" {
						m, _ := knn.NewItemKNN(s, cfg)
						return m
					}
					m, _ := knn.NewUserKNN(s, cfg)
					return m
				}
				s := newStore(t, first)
				inc := build(s)
				if err := inc.Refresh(ctx); err != nil { // first Refresh fits
					t.Fatal(err)
				}
				if err := inc.Refresh(ctx); err != nil { // nothing changed
					t.Fatal(err)
				}
				if _, err := s.AddBatch(ctx, later); err != nil {
					t.Fatal(err)
				}
				if err := inc.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				full := build(s)
				if err := full.Fit(ctx); err != nil {
					t.Fatal(err)
				}
				prefix, n := "p", 30
				ref := func(i int) recoant.Ref { return p(fmt.Sprint(prefix, i)) }
				if kind == "user" {
					n = 40
					ref = func(i int) recoant.Ref { return recoant.U(fmt.Sprint("u", i)) }
				}
				for i := range n {
					a, b := inc.Neighbors(ref(i)), full.Neighbors(ref(i))
					if len(a) != len(b) {
						t.Fatalf("%v: %d neighbors after Refresh, %d after Fit", ref(i), len(a), len(b))
					}
					for k := range a {
						if a[k].Ref != b[k].Ref || float32(a[k].Score) != float32(b[k].Score) {
							t.Fatalf("%v #%d: %v vs %v", ref(i), k, a[k], b[k])
						}
					}
				}
			})
		}
	}
}

// Refresh with small lists keeps them short and sorted.
func TestRefreshTruncated(t *testing.T) {
	s := newStore(t, history)
	m, _ := knn.NewItemKNN(s, knn.Config{Relation: rel, Neighbors: 1})
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.Add(sig("u7", "a", recoant.Purchase, 1))
	_ = s.Add(sig("u7", "new", recoant.Purchase, 1))
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "new"} {
		if n := m.Neighbors(p(id)); len(n) > 1 {
			t.Errorf("%s has %d neighbors", id, len(n))
		}
	}
	if got := ids(m.Neighbors(p("new"))); !slices.Equal(got, []string{"a"}) {
		t.Errorf("Neighbors(new) = %v", got)
	}
}

func TestRejected(t *testing.T) {
	s := testfix.NegativeStore(t)
	for _, sim := range []knn.Similarity{knn.Cosine, knn.Jaccard} {
		im, _ := knn.NewItemKNN(s, knn.Config{Relation: testfix.Rel, Similarity: sim})
		um, _ := knn.NewUserKNN(s, knn.Config{Relation: testfix.Rel, Similarity: sim})
		for _, m := range []interface {
			testfix.Recommender
			Fit(context.Context) error
		}{im, um} {
			if err := m.Fit(ctx); err != nil {
				t.Fatal(err)
			}
			testfix.CheckRejected(t, m)
		}
	}
}
