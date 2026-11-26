package recutil

import (
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

func TestRecutil(t *testing.T) {
	rel := store.Relation{Subject: recoant.User, Object: "item"}
	item := func(id string) recoant.Ref { return recoant.Ref{Kind: "item", ID: id} }
	cases := []struct {
		subject recoant.Ref
		target  recoant.Kind
		want    Direction
	}{
		{recoant.U("u"), "item", SubjectToObject},
		{item("a"), "item", ObjectToObject},
		{recoant.U("u"), recoant.User, SubjectToSubject},
		{item("a"), recoant.User, Unsupported},
	}
	for _, c := range cases {
		if got := DirectionOf(rel, recoant.Request{Subject: c.subject, Target: c.target}); got != c.want {
			t.Errorf("%v→%s = %v, want %v", c.subject, c.target, got, c.want)
		}
	}
	s, _ := store.New(signal.DefaultModel())
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = s.Add(recoant.Signal{Subject: recoant.U("u"), Object: item(id), Type: recoant.Visit, Value: 1, Time: time.Unix(1, 0)})
	}
	idx := func(id string) uint32 { i, _ := s.Index(item(id)); return i }
	req := recoant.Request{Subject: item("a"), Target: "item", K: 3, Exclude: []recoant.Ref{item("b"), item("zz")},
		Filter: func(r recoant.Ref) bool { return r.ID != "c" }}
	req.ExcludeSeen = true
	admit := Admit(s, req, []sparse.Entry{{Col: idx("d"), Val: 1}})
	for id, want := range map[string]bool{"a": false, "b": false, "c": false, "d": false} {
		if admit(idx(id)) != want {
			t.Errorf("admit(%s) != %v", id, want)
		}
	}
	if admit(99) {
		t.Error("unknown index admitted through filter")
	}
	req.Filter = nil
	if !Admit(s, req, nil)(idx("c")) {
		t.Error("c should be admitted without filter")
	}
	// Rejected (negative) entries are excluded unless IncludeRejected.
	req.ExcludeSeen = false
	hist := []sparse.Entry{{Col: idx("c"), Val: -1}, {Col: idx("d"), Val: 2}}
	if Admit(s, req, hist)(idx("c")) || !Admit(s, req, hist)(idx("d")) {
		t.Error("rejected c admitted or seen d excluded")
	}
	req.IncludeRejected = true
	if !Admit(s, req, hist)(idx("c")) {
		t.Error("IncludeRejected ignored")
	}
	res := Results(s, "item", []topk.Item{{Index: idx("c"), Score: 2}, {Index: 99, Score: 1}})
	if len(res) != 1 || res[0].Ref != item("c") || res[0].Score != 2 {
		t.Errorf("Results = %v", res)
	}
}
