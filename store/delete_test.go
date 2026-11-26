package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

func likesModel(retention time.Duration) signal.Model {
	return signal.Model{
		Rules: map[recoant.SignalType]signal.Rule{
			"like":  {Weight: 1, Relation: "likes"},
			"pass":  {Weight: 1, Relation: "likes", Feedback: signal.Negative},
			"stars": {Weight: 1, Relation: "rates", Feedback: signal.Rating, Neutral: 3},
			"score": {Weight: 1, Relation: "rates", Channel: "avg"},
		},
		Channels: map[string]signal.Channel{"avg": {Aggregate: signal.Mean, NoDecay: true}},
		Relations: map[string]signal.RelationSpec{
			"likes": {Subject: recoant.User, Object: recoant.User, Retention: retention},
			"rates": {Subject: recoant.User, Object: "product"},
		},
	}
}

func like(from, to string, typ recoant.SignalType, at time.Duration) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: typ, Value: 1, Time: t0.Add(at)}
}

func rate(user, product string, typ recoant.SignalType, v float64) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(user), Object: p(product), Type: typ, Value: v, Time: t0}
}

func fill(t *testing.T, s *store.Store, sigs ...recoant.Signal) {
	t.Helper()
	if _, err := s.AddBatch(context.Background(), sigs); err != nil {
		t.Fatal(err)
	}
}

func TestDelete(t *testing.T) {
	s, err := store.New(likesModel(0))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s,
		like("a", "b", "like", 0), like("b", "a", "like", 0), like("c", "a", "pass", 0), like("c", "b", "like", 0),
		rate("a", "x", "stars", 5), rate("a", "x", "score", 4), rate("b", "x", "score", 2),
	)
	likes := store.Relation{Name: "likes", Subject: recoant.User, Object: recoant.User}
	ia, _ := s.Index(recoant.U("a"))
	ib, _ := s.Index(recoant.U("b"))
	ic, _ := s.Index(recoant.U("c"))
	seq := s.Seq()
	if !s.Delete(recoant.U("a")) || s.Delete(recoant.U("a")) || s.Delete(p("nope")) || s.Delete(recoant.Ref{Kind: "brand", ID: "z"}) {
		t.Fatal("Delete result")
	}
	if s.Has(recoant.U("b"), recoant.U("a")) || s.Has(recoant.U("c"), recoant.U("a")) || len(s.Row(likes, ia, time.Time{})) != 0 {
		t.Error("signals of a deleted user remain")
	}
	if !s.Has(recoant.U("c"), recoant.U("b")) {
		t.Error("unrelated pair lost")
	}
	if _, ok := s.Index(recoant.U("a")); ok {
		t.Error("deleted ID still known")
	}
	if _, ok := s.Ref(recoant.User, ia); ok {
		t.Error("Ref of a deleted index")
	}
	if got := s.Interactions(recoant.U("b"), "product", time.Time{}); len(got) != 1 || got[0].Score != 1 {
		t.Errorf("mean channel of others changed: %v", got)
	}
	subjects, objects := s.Changes(likes, seq)
	if !slices.Contains(subjects, ia) || !slices.Contains(subjects, ib) || !slices.Contains(subjects, ic) || !slices.Contains(objects, ia) || !slices.Contains(objects, ib) {
		t.Errorf("Changes after delete: %v %v", subjects, objects)
	}
	del := s.Deleted(recoant.User)
	if del == nil || !del(ia) || del(ib) || del(1000) || s.Deleted("product") != nil {
		t.Error("Deleted")
	}
	if st := s.Stats(); st.Interactions != 2 { // c→b like, b→x score
		t.Errorf("Interactions = %d", st.Interactions)
	}
	fill(t, s, like("a", "c", "like", 0))
	if i, _ := s.Index(recoant.U("a")); i == ia {
		t.Error("re-added ID reuses the tombstone")
	}

	back, err := store.FromSnapshot(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if del := back.Deleted(recoant.User); del == nil || !del(ia) {
		t.Error("tombstone lost in snapshot")
	}
	if !back.Has(recoant.U("a"), recoant.U("c")) || back.Has(recoant.U("b"), recoant.U("a")) {
		t.Error("restored content")
	}
}

func TestDeleteObject(t *testing.T) {
	s, err := store.New(likesModel(0))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, rate("a", "x", "score", 4), rate("a", "y", "score", 2), rate("b", "x", "stars", 1))
	if !s.Delete(p("x")) {
		t.Fatal("Delete")
	}
	got := s.Interactions(recoant.U("a"), "product", time.Time{})
	if len(got) != 1 || got[0].Ref.ID != "y" || got[0].Score != 1 {
		t.Errorf("Interactions = %v", got)
	}
	if s.Rejected(recoant.U("b"), p("x")) {
		t.Error("rating of a deleted product remains")
	}
}

func TestExpire(t *testing.T) {
	s, err := store.New(likesModel(48 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s,
		like("a", "b", "like", 0), like("a", "b", "pass", 0),
		like("a", "c", "like", 0), like("a", "c", "like", 72*time.Hour),
		like("b", "c", "like", 24*time.Hour),
		rate("a", "x", "stars", 4),
	)
	likes := store.Relation{Name: "likes", Subject: recoant.User, Object: recoant.User}
	ia, _ := s.Index(recoant.U("a"))
	ib, _ := s.Index(recoant.U("b"))
	seq := s.Seq()
	if n := s.Expire(time.Time{}); n != 1 { // a→b in all channels; cutoff 72h − 48h
		t.Fatalf("Expire = %d, want 1", n)
	}
	if s.Has(recoant.U("a"), recoant.U("b")) || !s.Has(recoant.U("b"), recoant.U("c")) || !s.Has(recoant.U("a"), recoant.U("c")) {
		t.Error("wrong pairs expired")
	}
	if !s.Has(recoant.U("a"), p("x")) {
		t.Error("relation without retention expired")
	}
	if subjects, objects := s.Changes(likes, seq); !slices.Contains(subjects, ia) || !slices.Contains(objects, ib) {
		t.Errorf("Changes after expire: %v %v", subjects, objects)
	}
	if s.Expire(time.Time{}) != 0 || s.Seq() != seq+1 {
		t.Error("second Expire changed something")
	}

	back, err := store.FromSnapshot(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if back.Expire(t0.Add(70*time.Hour)) != 0 || back.Expire(t0.Add(73*time.Hour)) != 1 {
		t.Error("stamps not restored")
	}

	sn := s.Snapshot()
	sn.Version = 2
	for i := range sn.Relations {
		sn.Relations[i].Stamps = nil
	}
	old, err := store.FromSnapshot(sn)
	if err != nil {
		t.Fatal(err)
	}
	if old.Expire(t0.Add(119*time.Hour)) != 0 || old.Expire(t0.Add(121*time.Hour)) != 2 {
		t.Error("version 2 snapshot must stamp pairs with the latest signal")
	}

	sn = s.Snapshot()
	for i := range sn.Relations {
		if sn.Relations[i].Stamps != nil {
			sn.Relations[i].Stamps.Times = nil
		}
	}
	if _, err := store.FromSnapshot(sn); err == nil {
		t.Error("inconsistent stamps accepted")
	}
	sn.Version = 4
	if _, err := store.FromSnapshot(sn); err == nil {
		t.Error("future version accepted")
	}
}

func TestHistory(t *testing.T) {
	s, err := store.New(likesModel(0))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, like("a", "b", "like", 0), like("a", "c", "pass", 0), rate("a", "x", "stars", 1))
	h := s.History(recoant.U("a"), recoant.User)
	if len(h) != 2 || h["b"] || !h["c"] {
		t.Errorf("History = %v", h)
	}
	if h := s.History(recoant.U("a"), "product"); len(h) != 1 || !h["x"] {
		t.Errorf("History products = %v", h)
	}
	if s.History(recoant.U("nobody"), recoant.User) != nil {
		t.Error("History of an unknown subject")
	}
}

// Deletion and expiry in a relation with a retention and a mean channel.
func TestDeleteAndExpireWithStamps(t *testing.T) {
	m := signal.Model{
		Rules: map[recoant.SignalType]signal.Rule{
			"like":  {Weight: 1, Relation: "likes"},
			"score": {Weight: 1, Relation: "likes", Channel: "avg", Transform: signal.Transform{Kind: signal.Linear}},
		},
		Channels:  map[string]signal.Channel{"avg": {Aggregate: signal.Mean, NoDecay: true}},
		Relations: map[string]signal.RelationSpec{"likes": {Subject: recoant.User, Object: recoant.User, Retention: 48 * time.Hour}},
	}
	s, err := store.New(m)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s,
		like("a", "b", "like", 0), like("a", "b", "score", 0),
		like("a", "c", "score", 0), like("c", "a", "like", 0),
		like("b", "c", "like", 72*time.Hour), like("d", "c", "score", 72*time.Hour),
	)
	// Expire drops a→b, a→c and c→a, also from the mean channel.
	if n := s.Expire(time.Time{}); n != 3 {
		t.Fatalf("Expire = %d, want 3", n)
	}
	if s.Has(recoant.U("a"), recoant.U("c")) || !s.Has(recoant.U("d"), recoant.U("c")) {
		t.Error("mean channel after expiry")
	}
	// Deleting c removes its stamps as subject and object.
	fill(t, s, like("c", "b", "like", 72*time.Hour))
	if !s.Delete(recoant.U("c")) {
		t.Fatal("Delete")
	}
	back, err := store.FromSnapshot(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if back.Has(recoant.U("b"), recoant.U("c")) || back.Expire(t0.Add(200*time.Hour)) != 0 {
		t.Error("stamps of a deleted reference remain")
	}
}

func TestInteractionsEdgeCases(t *testing.T) {
	s, err := store.New(likesModel(0))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, like("a", "b", "like", 0), like("a", "c", "like", 0), rate("b", "x", "stars", 4))
	if s.Interactions(recoant.U("a"), "product", time.Time{}) != nil {
		t.Error("Interactions without relation to the kind")
	}
	// Equal scores are ordered by ID.
	got := s.Interactions(recoant.U("a"), recoant.User, time.Time{})
	if len(got) != 2 || got[0].Ref.ID != "b" || got[1].Ref.ID != "c" {
		t.Errorf("tie order: %v", got)
	}
	// Relations are sorted by subject, object, then name.
	rels := s.Relations()
	if len(rels) != 2 || rels[0].Object != "product" || rels[1].Object != recoant.User {
		t.Errorf("Relations = %v", rels)
	}
}

func TestSnapshotInvalidBounds(t *testing.T) {
	s, err := store.New(likesModel(0))
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, like("a", "b", "like", 0), like("b", "a", "like", 0))
	sn := s.Snapshot()
	ch := &sn.Relations[0].Channels[0]
	ch.Indptr = []int{0, 2, 1}
	ch.Indices = []uint32{0}
	ch.Values = []float32{1}
	ch.Indptr[2] = 1
	if _, err := store.FromSnapshot(sn); !errors.Is(err, store.ErrInvalidSnapshot) {
		t.Errorf("decreasing indptr: %v", err)
	}
}

func TestRelationsSortedByName(t *testing.T) {
	m := signal.Model{
		Rules: map[recoant.SignalType]signal.Rule{"like": {Weight: 1, Relation: "likes"}, "follow": {Weight: 1, Relation: "follows"}, "by": {Weight: 1}},
		Relations: map[string]signal.RelationSpec{
			"likes":   {Subject: recoant.User, Object: recoant.User},
			"follows": {Subject: recoant.User, Object: recoant.User},
		},
	}
	s, err := store.New(m)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, s, like("a", "b", "like", 0), like("a", "b", "follow", 0),
		recoant.Signal{Subject: p("x"), Object: recoant.Ref{Kind: "brand", ID: "z"}, Type: "by", Value: 1, Time: t0})
	if rels := s.Relations(); len(rels) != 3 || rels[0].Subject != "product" || rels[1].Name != "follows" || rels[2].Name != "likes" {
		t.Errorf("Relations = %v", rels)
	}
}
