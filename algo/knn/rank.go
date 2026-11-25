package knn

import (
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/store"
)

// recoantZero is the zero time: the store's latest signal.
var recoantZero time.Time

// rank turns candidate scores into the top-k references of the target kind,
// applying the request's exclusions and filter.
func rank(s *store.Store, req recoant.Request, scores map[uint32]float64, seen func(uint32) bool) []recoant.Scored {
	excluded := make(map[uint32]bool, len(req.Exclude)+1)
	for _, r := range append([]recoant.Ref{req.Subject}, req.Exclude...) {
		if r.Kind == req.Target {
			if i, ok := s.Index(r); ok {
				excluded[i] = true
			}
		}
	}
	tk := topk.New(req.K)
	for j, sc := range scores {
		if sc <= 0 || excluded[j] || seen(j) {
			continue
		}
		if req.Filter != nil {
			ref, ok := s.Ref(req.Target, j)
			if !ok || !req.Filter(ref) {
				continue
			}
		}
		tk.Push(j, sc)
	}
	items := tk.Sorted()
	out := make([]recoant.Scored, 0, len(items))
	for _, it := range items {
		if ref, ok := s.Ref(req.Target, it.Index); ok {
			out = append(out, recoant.Scored{Ref: ref, Score: it.Score})
		}
	}
	return out
}

func toScored(s *store.Store, kind recoant.Kind, list []Neighbor) []recoant.Scored {
	out := make([]recoant.Scored, 0, len(list))
	for _, n := range list {
		if ref, ok := s.Ref(kind, n.Index); ok {
			out = append(out, recoant.Scored{Ref: ref, Score: float64(n.Sim)})
		}
	}
	return out
}
