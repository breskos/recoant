package store_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func p(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

func sig(user, product string, typ recoant.SignalType, at time.Duration) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(user), Object: p(product), Type: typ, Value: 1, Time: t0.Add(at)}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4*math.Max(1, math.Abs(b)) }

func newStore(t *testing.T, hl time.Duration) *store.Store {
	t.Helper()
	m := signal.DefaultModel()
	m.HalfLife = hl
	s, err := store.New(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAddAndRead(t *testing.T) {
	s := newStore(t, time.Hour)
	for _, x := range []recoant.Signal{
		sig("u1", "a", recoant.Visit, 0),
		sig("u1", "a", recoant.Purchase, 0),
		sig("u1", "b", recoant.Click, time.Hour),
		sig("u2", "b", recoant.Visit, time.Hour),
		sig("u2", "b", "share", time.Hour), // ignored
		{Subject: recoant.U("u2"), Object: recoant.U("u1"), Type: recoant.Visit, Value: 1, Time: t0},
	} {
		if err := s.Add(x); err != nil {
			t.Fatal(err)
		}
	}
	now := t0.Add(time.Hour)
	if got := s.Strength(recoant.U("u1"), p("a"), now); !near(got, 5.5) {
		t.Errorf("Strength(u1,a) = %v, want 5.5", got)
	}
	if got := s.Strength(recoant.U("u1"), p("b"), now); !near(got, 2) {
		t.Errorf("Strength(u1,b) = %v, want 2", got)
	}
	if s.Strength(recoant.U("u3"), p("a"), now) != 0 || s.Strength(recoant.U("u2"), p("a"), now) != 0 {
		t.Error("unknown pairs must be 0")
	}
	if !s.Has(recoant.U("u2"), p("b")) || s.Has(recoant.U("u2"), p("a")) || s.Has(recoant.U("nobody"), p("a")) {
		t.Error("Has")
	}
	inter := s.Interactions(recoant.U("u1"), "product", now)
	if len(inter) != 2 || inter[0].Ref != p("a") || inter[1].Ref != p("b") {
		t.Errorf("Interactions = %v", inter)
	}
	if s.Interactions(recoant.U("nobody"), "product", now) != nil {
		t.Error("Interactions of unknown subject")
	}
	st := s.Stats()
	if st.Signals != 6 || st.Ignored != 1 || st.Interactions != 4 || !st.Latest.Equal(now) {
		t.Errorf("Stats = %+v", st)
	}
	if got := s.Kinds(); len(got) != 2 || got[0] != "product" || got[1] != recoant.User {
		t.Errorf("Kinds = %v", got)
	}
	rels := s.Relations()
	if len(rels) != 2 || rels[0].String() != "user→product" || rels[1] != (store.Relation{Subject: "user", Object: "user"}) {
		t.Errorf("Relations = %v", rels)
	}
	if s.Len("product") != 2 || s.Len("none") != 0 {
		t.Error("Len")
	}
	i, ok := s.Index(p("b"))
	if !ok {
		t.Fatal("Index")
	}
	if r, ok := s.Ref("product", i); !ok || r != p("b") {
		t.Error("Ref")
	}
	if _, ok := s.Ref("product", 99); ok {
		t.Error("Ref out of range")
	}
	if _, ok := s.Index(recoant.Ref{Kind: "none", ID: "x"}); ok {
		t.Error("Index of unknown kind")
	}
	if s.Model().HalfLife != time.Hour {
		t.Error("Model")
	}
}

func TestMatrix(t *testing.T) {
	s := newStore(t, 0)
	_, err := s.AddBatch(context.Background(), []recoant.Signal{
		sig("u1", "a", recoant.Visit, 0),
		sig("u2", "b", recoant.Purchase, 0),
		sig("u2", "a", recoant.Click, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Matrix(store.Relation{Subject: recoant.User, Object: "product"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Rows != 2 || m.Cols != 2 || m.NNZ() != 3 {
		t.Fatalf("shape %d×%d nnz %d", m.Rows, m.Cols, m.NNZ())
	}
	u2, _ := s.Index(recoant.U("u2"))
	b, _ := s.Index(p("b"))
	if m.At(int(u2), b) != 10 {
		t.Errorf("At = %v", m.At(int(u2), b))
	}
	if _, err := s.Matrix(store.Relation{Subject: "x", Object: "y"}, t0); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
}

func TestAddBatchErrors(t *testing.T) {
	s := newStore(t, 0)
	bad := []recoant.Signal{sig("u1", "a", recoant.Visit, 0), {}}
	n, err := s.AddBatch(context.Background(), bad)
	if n != 1 || !errors.Is(err, recoant.ErrInvalidSignal) {
		t.Errorf("AddBatch = %d, %v", n, err)
	}
	if err := s.Add(recoant.Signal{}); !errors.Is(err, recoant.ErrInvalidSignal) {
		t.Errorf("Add = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.AddBatch(ctx, bad); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	if _, err := store.New(signal.Model{HalfLife: -1}); err == nil {
		t.Error("invalid model accepted")
	}
}

// Over two years with a one-day half-life the epoch must move forward
// several times without losing precision or overflowing.
func TestRebase(t *testing.T) {
	s := newStore(t, 24*time.Hour)
	for d := range 730 {
		if err := s.Add(sig("u", "a", recoant.Visit, time.Duration(d)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	end := t0.Add(729 * 24 * time.Hour)
	// Geometric series 1 + 1/2 + 1/4 + … ≈ 2.
	if got := s.Strength(recoant.U("u"), p("a"), end); !near(got, 2) {
		t.Errorf("Strength = %v, want 2", got)
	}
}

func TestSnapshot(t *testing.T) {
	s := newStore(t, time.Hour)
	for _, x := range []recoant.Signal{
		sig("u1", "a", recoant.Visit, 0),
		sig("u2", "b", recoant.Purchase, time.Hour),
		sig("u2", "a", recoant.Click, 2*time.Hour),
	} {
		if err := s.Add(x); err != nil {
			t.Fatal(err)
		}
	}
	r, err := store.FromSnapshot(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	now := t0.Add(3 * time.Hour)
	for _, pair := range [][2]recoant.Ref{{recoant.U("u1"), p("a")}, {recoant.U("u2"), p("b")}, {recoant.U("u2"), p("a")}} {
		if a, b := s.Strength(pair[0], pair[1], now), r.Strength(pair[0], pair[1], now); a != b {
			t.Errorf("%v: %v != %v", pair, a, b)
		}
	}
	if s.Stats() != r.Stats() {
		t.Errorf("Stats %+v != %+v", s.Stats(), r.Stats())
	}
	if err := r.Add(sig("u3", "c", recoant.Visit, 3*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestFromSnapshotErrors(t *testing.T) {
	s := newStore(t, 0)
	if err := s.Add(sig("u1", "a", recoant.Visit, 0)); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(sn *store.Snapshot){
		"version":      func(sn *store.Snapshot) { sn.Version = 99 },
		"dup kind":     func(sn *store.Snapshot) { sn.Kinds = append(sn.Kinds, sn.Kinds[0]) },
		"dup id":       func(sn *store.Snapshot) { sn.Kinds[0].IDs = []string{"a", "a"} },
		"unknown kind": func(sn *store.Snapshot) { sn.Relations[0].Relation.Object = "none" },
		"dup relation": func(sn *store.Snapshot) { sn.Relations = append(sn.Relations, sn.Relations[0]) },
		"shape":        func(sn *store.Snapshot) { sn.Relations[0].Values = nil },
		"rows":         func(sn *store.Snapshot) { sn.Relations[0].Indptr = []int{0, 1, 1, 1} },
		"bounds":       func(sn *store.Snapshot) { sn.Relations[0].Indptr = []int{0, 1}; sn.Relations[0].Indptr[0] = 2 },
		"column":       func(sn *store.Snapshot) { sn.Relations[0].Indices[0] = 7 },
		"model":        func(sn *store.Snapshot) { sn.Model.HalfLife = -1 },
	}
	for name, mutate := range cases {
		sn := s.Snapshot()
		mutate(sn)
		if _, err := store.FromSnapshot(sn); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := store.FromSnapshot(nil); !errors.Is(err, store.ErrInvalidSnapshot) {
		t.Error("nil snapshot")
	}
}

func BenchmarkAdd(b *testing.B) {
	s, _ := store.New(signal.DefaultModel())
	sigs := make([]recoant.Signal, 1<<16)
	for i := range sigs {
		sigs[i] = recoant.Signal{
			Subject: recoant.U(strconv.Itoa(i % 5000)),
			Object:  p(strconv.Itoa(i * 31 % 20000)),
			Type:    recoant.Click, Value: 1, Time: t0.Add(time.Duration(i) * time.Second),
		}
	}
	b.ResetTimer()
	for i := range b.N {
		_ = s.Add(sigs[i%len(sigs)])
	}
}

func TestChangesAndRow(t *testing.T) {
	s := newStore(t, 0)
	rel := store.Relation{Subject: recoant.User, Object: "product"}
	if subj, obj := s.Changes(rel, 0); subj != nil || obj != nil {
		t.Fatal("changes of unknown relation")
	}
	_ = s.Add(sig("u1", "a", recoant.Visit, 0))
	_ = s.Add(sig("u2", "b", recoant.Visit, 0))
	seq := s.Seq()
	_ = s.Add(sig("u2", "c", recoant.Click, 0))
	_ = s.Add(sig("u2", "c", "share", 0)) // ignored: no change
	if s.Seq() != seq+1 {
		t.Fatalf("Seq = %d, want %d", s.Seq(), seq+1)
	}
	subj, obj := s.Changes(rel, seq)
	u2, _ := s.Index(recoant.U("u2"))
	c, _ := s.Index(p("c"))
	if len(subj) != 1 || subj[0] != u2 || len(obj) != 1 || obj[0] != c {
		t.Errorf("Changes = %v, %v", subj, obj)
	}
	if all, _ := s.Changes(rel, 0); len(all) != 2 {
		t.Errorf("Changes since 0 = %v", all)
	}
	row := s.Row(rel, u2, time.Time{})
	if len(row) != 2 || row[1].Col != c || row[1].Val != 2 {
		t.Errorf("Row = %v", row)
	}
	row[0].Val = 99 // a copy
	if s.Strength(recoant.U("u2"), p("b"), t0) != 1 {
		t.Error("Row is not a copy")
	}
	if s.Row(rel, 99, t0) != nil || s.Row(store.Relation{Subject: "x", Object: "y"}, 0, t0) != nil {
		t.Error("Row of unknown subject or relation")
	}
}
