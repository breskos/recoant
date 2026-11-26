package knn

import (
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/recutil"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/store"
)

// recoantZero is the zero time: the store's latest signal.
var recoantZero time.Time

// rank turns candidate scores into the top-k references of the target kind,
// applying the request's exclusions and filter.
func rank(s *store.Store, req recoant.Request, scores map[uint32]float64, seen func(uint32) bool) []recoant.Scored {
	admit := recutil.Admit(s, req, seen)
	tk := topk.New(req.K)
	for j, sc := range scores {
		if sc > 0 && admit(j) {
			tk.Push(j, sc)
		}
	}
	return recutil.Results(s, req.Target, tk.Sorted())
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
