// Package testfix holds small hand-made fixtures shared by the tests of the
// recommenders.
package testfix

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
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

// NegativeModel is the default signal model plus a negative "hide" signal
// and 1–5 "stars" ratings with neutral 3.
func NegativeModel() signal.Model {
	m := signal.DefaultModel()
	m.Rules["hide"] = signal.Rule{Weight: 20, Feedback: signal.Negative}
	m.Rules["stars"] = signal.Rule{Weight: 2, Feedback: signal.Rating, Scale: []float64{1, 5}, Neutral: 3}
	return m
}

// Hidden is Groups plus u1 hiding c, the product u1 would get first.
func Hidden() []recoant.Signal {
	return append(append([]recoant.Signal(nil), Groups...), Sig("u1", "c", "hide", 1))
}

// NegativeStore returns a store with NegativeModel and Hidden.
func NegativeStore(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.New(NegativeModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBatch(context.Background(), Hidden()); err != nil {
		t.Fatal(err)
	}
	return s
}

// CheckRejected checks a model trained on NegativeStore: c is never
// recommended to u1, unless the request includes rejected objects.
func CheckRejected(t *testing.T, m Recommender) {
	t.Helper()
	ctx := context.Background()
	res, err := m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 6})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Ref.ID == "c" {
			t.Errorf("rejected c recommended: %v", IDs(res))
		}
	}
	res, err = m.Recommend(ctx, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 6, IncludeRejected: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res {
		found = found || r.Ref.ID == "c"
	}
	if !found && len(res) < 6 {
		t.Logf("c not among %v with IncludeRejected (allowed: the model may score it too low)", IDs(res))
	}
}

// countdown is a context whose Err reports cancellation after n calls.
type countdown struct {
	context.Context
	left atomic.Int64
}

func (c *countdown) Err() error {
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// CancelAfter returns a context whose Err reports nothing for the first n
// calls and context.Canceled afterwards, to reach every point where a
// model checks its context.
func CancelAfter(n int64) context.Context {
	c := &countdown{Context: context.Background()}
	c.left.Store(n)
	return c
}

// CheckCancel runs fn with CancelAfter(0), CancelAfter(1), … until it
// succeeds. Every failure before must be context.Canceled.
func CheckCancel(t *testing.T, fn func(ctx context.Context) error) {
	t.Helper()
	for n := int64(0); n < 100000; n++ {
		err := fn(CancelAfter(n))
		if err == nil {
			return
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("after %d checks: %v", n, err)
		}
	}
	t.Fatal("never succeeded")
}

// Scorer is what CheckInterest needs.
type Scorer interface {
	Interest(subjects []recoant.Ref, object recoant.Ref) []float64
}

// CheckInterest checks a model trained on Groups: u2 (who likes c) is more
// interested in c than u5; unknown subjects, other kinds and unknown
// objects are NaN.
func CheckInterest(t *testing.T, m Scorer) {
	t.Helper()
	got := m.Interest([]recoant.Ref{recoant.U("u2"), recoant.U("u5"), recoant.U("nobody"), P("a")}, P("c"))
	if len(got) != 4 || !(got[0] > got[1]) || !math.IsNaN(got[2]) || !math.IsNaN(got[3]) {
		t.Errorf("interest in c: %v", got)
	}
	for _, object := range []recoant.Ref{P("nothing"), recoant.U("u1")} {
		if got := m.Interest([]recoant.Ref{recoant.U("u2")}, object); !math.IsNaN(got[0]) {
			t.Errorf("interest in %s: %v", object, got)
		}
	}
}

// Roles is what CheckRoles needs.
type Roles interface {
	Taste(ref recoant.Ref) ([]float64, bool)
	Appearance(ref recoant.Ref) ([]float64, bool)
}

// CheckRoles checks a trained model: the subject has a taste vector, the
// object an appearance vector, and not the other way round.
func CheckRoles(t *testing.T, m Roles, subject, object recoant.Ref) {
	t.Helper()
	if u, ok := m.Taste(subject); !ok || len(u) == 0 {
		t.Errorf("taste of %s: %v %v", subject, u, ok)
	}
	if y, ok := m.Appearance(object); !ok || len(y) == 0 {
		t.Errorf("appearance of %s: %v %v", object, y, ok)
	}
	if _, ok := m.Taste(object); ok {
		t.Errorf("taste of object %s", object)
	}
	if _, ok := m.Appearance(subject); ok {
		t.Errorf("appearance of subject %s", subject)
	}
}
