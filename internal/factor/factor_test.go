package factor_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/embed"
	"github.com/breskos/recoant/internal/factor"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/sparse"
)

var ctx = context.Background()

// view uses one-hot vectors: product a, b, c → dimension 0; x, y, z → 1.
func view(t *testing.T) factor.View {
	s := testfix.Store(t, testfix.Groups)
	items := embed.New(s.Len("product"), 2)
	for _, id := range []string{"a", "b", "c", "x", "y", "z"} {
		i, _ := s.Index(testfix.P(id))
		d := 0
		if id >= "x" {
			d = 1
		}
		items.Row(int(i))[d] = 1
		if id == "c" || id == "z" {
			items.Row(int(i))[d] = 2
		}
	}
	users := embed.New(s.Len(recoant.User), 2)
	for u := range users.N {
		ref, _ := s.Ref(recoant.User, uint32(u))
		d := 0
		if ref.ID >= "u4" {
			d = 1
		}
		users.Row(u)[d] = 1
	}
	return factor.View{
		Store: s, Rel: testfix.Rel, Items: items, Users: users, UserNorms: users.Norms(),
		UserVector: func(si uint32, history []sparse.Entry) ([]float64, bool) {
			if len(history) == 0 {
				return nil, false
			}
			return users.Row(int(si)), true
		},
	}
}

func TestRecommend(t *testing.T) {
	v := view(t)
	testfix.CheckGroups(t, recommender(v), true)
	v.SimItems = v.Items
	v.SimItemNorms = v.Items.Norms()
	res, _ := factor.Recommend(ctx, v, recoant.Request{Subject: testfix.P("x"), Target: "product", K: 1})
	if got := testfix.IDs(res); len(got) != 1 || got[0] == "a" {
		t.Errorf("similar to x: %v", got)
	}
	_ = v.Store.Add(testfix.Sig("u1", "late", recoant.Visit, 1)) // object without vector
	if res, err := factor.Recommend(ctx, v, recoant.Request{Subject: testfix.P("late"), Target: "product", K: 1}); res != nil || err != nil {
		t.Errorf("object without vector: %v %v", res, err)
	}
	if res, err := factor.Recommend(ctx, v, recoant.Request{Subject: recoant.U("nobody"), Target: "product", K: 1}); res != nil || err != nil {
		t.Errorf("unknown subject: %v %v", res, err)
	}
	_ = v.Store.Add(recoant.Signal{Subject: recoant.U("other"), Object: recoant.Ref{Kind: "movie", ID: "m"}, Type: recoant.Visit, Value: 1, Time: testfix.T0})
	if res, err := factor.Recommend(ctx, v, recoant.Request{Subject: recoant.U("other"), Target: "product", K: 1}); res != nil || err != nil {
		t.Errorf("subject without history: %v %v", res, err)
	}
	empty := v
	empty.Items = nil
	if res, err := factor.Recommend(ctx, empty, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 1}); res != nil || err != nil {
		t.Errorf("no items: %v %v", res, err)
	}
}

func TestErrors(t *testing.T) {
	v := view(t)
	if _, err := factor.Recommend(ctx, v, recoant.Request{}); !errors.Is(err, recoant.ErrInvalidRequest) {
		t.Errorf("invalid: %v", err)
	}
	if _, err := factor.Recommend(ctx, v, recoant.Request{Subject: testfix.P("a"), Target: recoant.User, K: 1}); !errors.Is(err, recoant.ErrUnsupported) {
		t.Errorf("unsupported: %v", err)
	}
	v.Users = nil
	if _, err := factor.Recommend(ctx, v, recoant.Request{Subject: recoant.U("u1"), Target: recoant.User, K: 1}); !errors.Is(err, recoant.ErrUnsupported) {
		t.Errorf("no users: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := factor.Recommend(canceled, v, recoant.Request{Subject: recoant.U("u1"), Target: "product", K: 1}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

type recommender factor.View

func (r recommender) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	return factor.Recommend(ctx, factor.View(r), req)
}

func TestInterest(t *testing.T) {
	v := view(t)
	v.ItemBias = make([]float64, v.Items.N)
	testfix.CheckInterest(t, scorer(v))
	_ = v.Store.Add(testfix.Sig("u1", "late", recoant.Visit, 1)) // object without vector
	_ = v.Store.Add(testfix.Sig("late-user", "a", recoant.Visit, 1))
	if got := factor.Interest(v, []recoant.Ref{recoant.U("u1")}, testfix.P("late")); !math.IsNaN(got[0]) {
		t.Errorf("object without vector: %v", got)
	}
	if got := factor.Interest(v, []recoant.Ref{recoant.U("late-user")}, testfix.P("a")); !math.IsNaN(got[0]) {
		t.Errorf("subject without vector: %v", got)
	}
	v.Users = nil
	if got := factor.Interest(v, []recoant.Ref{recoant.U("u1")}, testfix.P("a")); !math.IsNaN(got[0]) {
		t.Errorf("no subject vectors: %v", got)
	}
}

type scorer factor.View

func (s scorer) Interest(subjects []recoant.Ref, object recoant.Ref) []float64 {
	return factor.Interest(factor.View(s), subjects, object)
}

func TestRoles(t *testing.T) {
	v := view(t)
	if u, ok := factor.Taste(v, recoant.U("u4")); !ok || u[1] != 1 {
		t.Errorf("taste of u4: %v %v", u, ok)
	}
	if y, ok := factor.Appearance(v, testfix.P("c")); !ok || y[0] != 2 {
		t.Errorf("appearance of c: %v %v", y, ok)
	}
	for _, ref := range []recoant.Ref{testfix.P("a"), recoant.U("nobody")} {
		if _, ok := factor.Taste(v, ref); ok {
			t.Errorf("taste of %s", ref)
		}
	}
	_ = v.Store.Add(testfix.Sig("u1", "late", recoant.Visit, 1))
	if _, ok := factor.Appearance(v, testfix.P("late")); ok {
		t.Error("appearance without vector")
	}
	v.Users = nil
	if _, ok := factor.Taste(v, recoant.U("u1")); ok {
		t.Error("taste without vectors")
	}
}
