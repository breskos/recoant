// Package testfix holds small hand-made fixtures shared by the tests of the
// recommenders.
package testfix

import (
	"context"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// T0 is the time of all fixture signals.
var T0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Rel is the fixture relation user→product.
var Rel = store.Relation{Subject: recoant.User, Object: "product"}

// P returns a product reference.
func P(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

// Sig returns a user→product signal at T0.
func Sig(user, product string, typ recoant.SignalType, value float64) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(user), Object: P(product), Type: typ, Value: value, Time: T0}
}

// Groups has two groups: users u1–u3 like a, b, c; users u4–u6 like x, y,
// z. u1 has not seen c yet; u4 has not seen z yet.
var Groups = []recoant.Signal{
	Sig("u1", "a", recoant.Purchase, 1), Sig("u1", "b", recoant.Click, 1),
	Sig("u2", "a", recoant.Click, 1), Sig("u2", "b", recoant.Purchase, 1), Sig("u2", "c", recoant.Click, 1),
	Sig("u3", "b", recoant.Click, 1), Sig("u3", "c", recoant.Purchase, 1), Sig("u3", "a", recoant.Visit, 1),
	Sig("u4", "x", recoant.Purchase, 1), Sig("u4", "y", recoant.Click, 1),
	Sig("u5", "x", recoant.Click, 1), Sig("u5", "y", recoant.Click, 1), Sig("u5", "z", recoant.Purchase, 1),
	Sig("u6", "y", recoant.Click, 1), Sig("u6", "z", recoant.Click, 1), Sig("u6", "x", recoant.Visit, 1),
}

// Store returns a store with the default signal model and the signals.
func Store(t testing.TB, sigs []recoant.Signal) *store.Store {
	t.Helper()
	s, err := store.New(signal.DefaultModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBatch(context.Background(), sigs); err != nil {
		t.Fatal(err)
	}
	return s
}

// IDs returns the IDs of results.
func IDs(res []recoant.Scored) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Ref.ID
	}
	return out
}

// Recommender is what the shared checks need.
type Recommender interface {
	Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error)
}

// CheckGroups checks a model trained on Groups: u1 gets c first, u4 gets z
// first; the most similar product of a is from its group; the most similar
// user of u1 is from its group.
func CheckGroups(t *testing.T, m Recommender, userToUser bool) {
	t.Helper()
	ctx := context.Background()
	for user, want := range map[string]string{"u1": "c", "u4": "z"} {
		res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U(user), Target: "product", K: 3, ExcludeSeen: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) == 0 || res[0].Ref.ID != want {
			t.Errorf("%s: %v, want %s first", user, IDs(res), want)
		}
	}
	res, err := m.Recommend(ctx, recoant.Request{Subject: P("a"), Target: "product", K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || (res[0].Ref.ID != "b" && res[0].Ref.ID != "c") {
		t.Errorf("similar to a: %v", IDs(res))
	}
	res, err = m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: recoant.User, K: 1})
	if !userToUser {
		if err == nil {
			t.Error("user-to-user should be unsupported")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || (res[0].Ref.ID != "u2" && res[0].Ref.ID != "u3") {
		t.Errorf("similar to u1: %v", IDs(res))
	}
}
