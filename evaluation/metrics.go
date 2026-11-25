package evaluation

import (
	"math"

	"github.com/breskos/recoant"
)

// Relevant is the set of relevant objects of one subject.
type Relevant map[recoant.Ref]struct{}

// NewRelevant returns a set of the given references.
func NewRelevant(refs ...recoant.Ref) Relevant {
	r := make(Relevant, len(refs))
	for _, x := range refs {
		r[x] = struct{}{}
	}
	return r
}

func top(recs []recoant.Ref, k int) []recoant.Ref { return recs[:min(k, len(recs))] }

func hits(recs []recoant.Ref, rel Relevant, k int) int {
	n := 0
	for _, x := range top(recs, k) {
		if _, ok := rel[x]; ok {
			n++
		}
	}
	return n
}

// Precision returns the share of the first k recommendations that are
// relevant. Missing recommendations count as misses.
func Precision(recs []recoant.Ref, rel Relevant, k int) float64 {
	if k <= 0 {
		return 0
	}
	return float64(hits(recs, rel, k)) / float64(k)
}

// Recall returns the share of relevant objects among the first k
// recommendations.
func Recall(recs []recoant.Ref, rel Relevant, k int) float64 {
	if len(rel) == 0 {
		return 0
	}
	return float64(hits(recs, rel, k)) / float64(len(rel))
}

// HitRate returns 1 if at least one of the first k recommendations is
// relevant, else 0.
func HitRate(recs []recoant.Ref, rel Relevant, k int) float64 {
	if hits(recs, rel, k) > 0 {
		return 1
	}
	return 0
}

// ReciprocalRank returns 1/rank of the first relevant recommendation within
// the first k, or 0.
func ReciprocalRank(recs []recoant.Ref, rel Relevant, k int) float64 {
	for i, x := range top(recs, k) {
		if _, ok := rel[x]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// NDCG returns the normalized discounted cumulative gain of the first k
// recommendations with binary relevance.
func NDCG(recs []recoant.Ref, rel Relevant, k int) float64 {
	var dcg, ideal float64
	for i, x := range top(recs, k) {
		if _, ok := rel[x]; ok {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	for i := range min(k, len(rel)) {
		ideal += 1 / math.Log2(float64(i+2))
	}
	if ideal == 0 {
		return 0
	}
	return dcg / ideal
}

// AveragePrecision returns the average of the precision at each relevant
// position within the first k, divided by min(k, number of relevant).
func AveragePrecision(recs []recoant.Ref, rel Relevant, k int) float64 {
	n := min(k, len(rel))
	if n == 0 {
		return 0
	}
	var sum float64
	found := 0
	for i, x := range top(recs, k) {
		if _, ok := rel[x]; ok {
			found++
			sum += float64(found) / float64(i+1)
		}
	}
	return sum / float64(n)
}
