package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// shopModel has implicit, rating and negative signals on the unnamed
// user→product relation.
func shopModel() signal.Model {
	m := signal.DefaultModel()
	m.HalfLife = 0
	m.Rules["stars"] = signal.Rule{Weight: 1, Feedback: signal.Rating, Scale: []float64{1, 5}, Neutral: 3}
	m.Rules["return"] = signal.Rule{Weight: 8, Feedback: signal.Negative}
	m.Rules["depth"] = signal.Rule{Weight: 1, Transform: signal.Transform{Kind: signal.Linear}, Channel: "depth"}
	m.Rules["score"] = signal.Rule{Weight: 1, Transform: signal.Transform{Kind: signal.Linear}, Channel: "score"}
	m.Channels = map[string]signal.Channel{
		"depth": {Aggregate: signal.Max},
		"score": {Aggregate: signal.Mean, NoDecay: true},
	}
	return m
}

func TestChannels(t *testing.T) {
	s, err := store.New(shopModel())
	if err != nil {
		t.Fatal(err)
	}
	u, a, b := recoant.U("u"), p("a"), p("b")
	add := func(product, typ string, v float64) {
		t.Helper()
		if err := s.Add(recoant.Signal{Subject: u, Object: p(product), Type: recoant.SignalType(typ), Value: v, Time: t0}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "visit", 1)
	add("a", "stars", 2)
	add("a", "stars", 5) // replaces 2
	add("a", "stars", 9) // clipped to 5
	add("b", "purchase", 1)
	add("b", "return", 1)
	add("a", "depth", 2)
	add("a", "depth", 4)
	add("a", "depth", 3) // max stays 4
	add("a", "score", 1)
	add("a", "score", 3) // mean 2
	rel := store.Relation{Subject: recoant.User, Object: "product"}
	if got := s.Channels(rel); len(got) != 5 {
		t.Errorf("Channels = %v", got)
	}
	// a: visit 1 + (5 − 3) + depth 4 + score 2 = 9; b: purchase 10 − return 8 = 2.
	if got := s.Strength(u, a, t0); got != 9 {
		t.Errorf("Strength(a) = %v, want 9", got)
	}
	if got := s.Strength(u, b, t0); got != 2 {
		t.Errorf("Strength(b) = %v, want 2", got)
	}
	stars, err := s.ChannelMatrix(rel, "rating", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	ui, _ := s.Index(u)
	ai, _ := s.Index(a)
	if stars.At(int(ui), ai) != 5 || stars.NNZ() != 1 {
		t.Errorf("rating channel = %v", stars.Values)
	}
	score, _ := s.ChannelMatrix(rel, "score", time.Time{})
	if score.At(int(ui), ai) != 2 {
		t.Errorf("mean channel = %v", score.Values)
	}
	if _, err := s.ChannelMatrix(rel, "nope", time.Time{}); !errors.Is(err, store.ErrUnknownChannel) {
		t.Errorf("unknown channel: %v", err)
	}
	if _, err := s.ChannelMatrix(store.Relation{Subject: "x", Object: "y"}, "rating", time.Time{}); !errors.Is(err, store.ErrUnknownRelation) {
		t.Errorf("unknown relation: %v", err)
	}
	if s.Channels(store.Relation{Subject: "x", Object: "y"}) != nil {
		t.Error("Channels of unknown relation")
	}
	add("b", "return", 1) // now 10 − 16 < 0
	if !s.Rejected(u, b) || s.Rejected(u, a) || s.Rejected(u, p("zz")) {
		t.Error("Rejected")
	}
	m, _ := s.Matrix(rel, time.Time{})
	bi, _ := s.Index(b)
	if m.At(int(ui), bi) != -6 {
		t.Errorf("Matrix(b) = %v, want -6", m.At(int(ui), bi))
	}
	inter := s.Interactions(u, "product", t0)
	if len(inter) != 2 || inter[0].Ref != a || inter[1].Score != -6 {
		t.Errorf("Interactions = %v", inter)
	}
	if st := s.Stats(); st.Interactions != 2 {
		t.Errorf("Stats.Interactions = %d", st.Interactions)
	}
	// A snapshot keeps all channels.
	r, err := store.FromSnapshot(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []recoant.Ref{a, b} {
		if r.Strength(u, x, t0) != s.Strength(u, x, t0) {
			t.Errorf("snapshot differs for %v", x)
		}
	}
	bad := s.Snapshot()
	for i, c := range bad.Relations[0].Channels {
		if c.Name == "score" {
			bad.Relations[0].Channels[i].Counts = nil
		}
	}
	if _, err := store.FromSnapshot(bad); err == nil {
		t.Error("mean channel without counts accepted")
	}
}

func TestNamedRelations(t *testing.T) {
	m := signal.Model{
		Rules: map[recoant.SignalType]signal.Rule{
			"like":   {Weight: 5, Relation: "likes"},
			"pass":   {Weight: 3, Relation: "likes", Feedback: signal.Negative},
			"follow": {Weight: 1, Relation: "follows"},
		},
		Relations: map[string]signal.RelationSpec{"likes": {Subject: recoant.User, Object: recoant.User}},
	}
	s, err := store.New(m)
	if err != nil {
		t.Fatal(err)
	}
	sig := func(from, to, typ string) recoant.Signal {
		return recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: recoant.SignalType(typ), Value: 1, Time: t0}
	}
	for _, x := range []recoant.Signal{sig("a", "b", "like"), sig("a", "c", "pass"), sig("a", "b", "follow")} {
		if err := s.Add(x); err != nil {
			t.Fatal(err)
		}
	}
	likes := store.Relation{Name: "likes", Subject: recoant.User, Object: recoant.User}
	if rel, ok := s.RelationOf(sig("a", "b", "like")); !ok || rel != likes {
		t.Errorf("RelationOf = %v %v", rel, ok)
	}
	if rels := s.Relations(); len(rels) != 2 || rels[0].String() != "follows: user→user" {
		t.Errorf("Relations = %v", rels)
	}
	if got := s.Strength(recoant.U("a"), recoant.U("b"), t0); got != 6 {
		t.Errorf("Strength over both relations = %v, want 6", got)
	}
	if !s.Rejected(recoant.U("a"), recoant.U("c")) {
		t.Error("pass must reject")
	}
	mat, _ := s.Matrix(likes, time.Time{})
	if mat.NNZ() != 2 {
		t.Errorf("likes has %d entries", mat.NNZ())
	}
	wrong := recoant.Signal{Subject: recoant.U("a"), Object: p("x"), Type: "like", Value: 1, Time: t0}
	if err := s.Add(wrong); !errors.Is(err, recoant.ErrInvalidSignal) {
		t.Errorf("like on a product: %v", err)
	}
	if _, err := s.AddBatch(context.Background(), []recoant.Signal{wrong}); !errors.Is(err, recoant.ErrInvalidSignal) {
		t.Errorf("AddBatch like on a product: %v", err)
	}
}

// Snapshots written before v0.4 (version 1, one channel) can be restored.
func TestSnapshotVersion1(t *testing.T) {
	m := signal.DefaultModel()
	sn := &store.Snapshot{
		Version: 1,
		Model:   m,
		Epoch:   t0,
		Latest:  t0,
		Signals: 1,
		Kinds:   []store.KindSnapshot{{Kind: "product", IDs: []string{"a"}}, {Kind: recoant.User, IDs: []string{"u"}}},
		Relations: []store.RelationSnapshot{{
			Relation: store.Relation{Subject: recoant.User, Object: "product"},
			Indptr:   []int{0, 1}, Indices: []uint32{0}, Values: []float32{10},
		}},
	}
	s, err := store.FromSnapshot(sn)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Strength(recoant.U("u"), p("a"), t0); got != 10 {
		t.Errorf("Strength = %v", got)
	}
	if got := s.Channels(store.Relation{Subject: recoant.User, Object: "product"}); len(got) != 1 || got[0] != "positive" {
		t.Errorf("Channels = %v", got)
	}
}
