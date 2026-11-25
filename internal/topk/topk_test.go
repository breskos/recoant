package topk

import (
	"container/heap"
	"slices"
	"testing"
)

func TestTopK(t *testing.T) {
	tk := New(3)
	for i, s := range []float64{5, 1, 9, 7, 7, 3} {
		tk.Push(uint32(i), s)
	}
	got := tk.Sorted()
	want := []Item{{2, 9}, {3, 7}, {4, 7}}
	if !slices.Equal(got, want) {
		t.Fatalf("Sorted = %v, want %v", got, want)
	}
	zero := New(0)
	zero.Push(1, 1)
	if len(zero.Sorted()) != 0 {
		t.Fatal("k=0")
	}
}

func TestHeapPop(t *testing.T) {
	h := &minHeap{}
	heap.Push(h, Item{1, 3})
	heap.Push(h, Item{2, 1})
	if it := heap.Pop(h).(Item); it.Index != 2 {
		t.Fatalf("Pop = %v", it)
	}
	same := New(2)
	same.Push(1, 1)
	same.Push(1, 1)
	if got := same.Sorted(); len(got) != 2 {
		t.Fatalf("Sorted = %v", got)
	}
}
