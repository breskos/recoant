// Package topk selects the k highest scores from a stream of candidates in
// O(n log k).
package topk

import (
	"container/heap"
	"slices"
)

// Item is a candidate index with its score.
type Item struct {
	Index uint32
	Score float64
}

// less orders items from worst to best: lower score first, and on equal
// scores the higher index first, so lower indexes win ties.
func less(a, b Item) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.Index > b.Index
}

type minHeap []Item

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return less(h[i], h[j]) }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(Item)) }
func (h *minHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// TopK keeps the k best items pushed so far.
type TopK struct {
	k int
	h minHeap
}

// New returns a collector for the k best items.
func New(k int) *TopK { return &TopK{k: k, h: make(minHeap, 0, k)} }

// Push offers a candidate.
func (t *TopK) Push(index uint32, score float64) {
	it := Item{Index: index, Score: score}
	if len(t.h) < t.k {
		heap.Push(&t.h, it)
		return
	}
	if t.k > 0 && less(t.h[0], it) {
		t.h[0] = it
		heap.Fix(&t.h, 0)
	}
}

// Sorted returns the kept items, best first.
func (t *TopK) Sorted() []Item {
	out := slices.Clone(t.h)
	slices.SortFunc(out, func(a, b Item) int {
		switch {
		case less(b, a):
			return -1
		case less(a, b):
			return 1
		}
		return 0
	})
	return out
}
