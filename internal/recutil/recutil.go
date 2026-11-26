// Package recutil holds request handling shared by the recommenders:
// directions, exclusions, filters and result conversion.
package recutil

import (
	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

// Direction is what a request asks for, relative to a relation.
type Direction int

// Directions.
const (
	Unsupported      Direction = iota
	SubjectToObject            // e.g. products for a user
	ObjectToObject             // e.g. products similar to a product
	SubjectToSubject           // e.g. users similar to a user
)

// DirectionOf classifies a request. For relations whose subject and object
// kind are equal, SubjectToObject wins.
func DirectionOf(rel store.Relation, req recoant.Request) Direction {
	switch {
	case req.Subject.Kind == rel.Subject && req.Target == rel.Object:
		return SubjectToObject
	case req.Subject.Kind == rel.Object && req.Target == rel.Object:
		return ObjectToObject
	case req.Subject.Kind == rel.Subject && req.Target == rel.Subject:
		return SubjectToSubject
	}
	return Unsupported
}

// Admit returns a function that reports whether the target index j may be
// recommended: not the subject, not excluded, accepted by the request's
// filter and, given the subject's history row (nil if the target kind is
// not the history's object kind), not seen if ExcludeSeen and not rejected
// (negative value) unless IncludeRejected.
func Admit(s *store.Store, req recoant.Request, history []sparse.Entry) func(uint32) bool {
	excluded := make(map[uint32]bool, len(req.Exclude)+1)
	for _, r := range append([]recoant.Ref{req.Subject}, req.Exclude...) {
		if r.Kind == req.Target {
			if i, ok := s.Index(r); ok {
				excluded[i] = true
			}
		}
	}
	return func(j uint32) bool {
		if excluded[j] {
			return false
		}
		if history != nil {
			if v, ok := sparse.Get(history, j); ok && (req.ExcludeSeen || (v < 0 && !req.IncludeRejected)) {
				return false
			}
		}
		if req.Filter != nil {
			ref, ok := s.Ref(req.Target, j)
			return ok && req.Filter(ref)
		}
		return true
	}
}

// Results converts ranked indexes of a kind into references.
func Results(s *store.Store, kind recoant.Kind, items []topk.Item) []recoant.Scored {
	out := make([]recoant.Scored, 0, len(items))
	for _, it := range items {
		if ref, ok := s.Ref(kind, it.Index); ok {
			out = append(out, recoant.Scored{Ref: ref, Score: it.Score})
		}
	}
	return out
}
