package knn

import "testing"

// top must not keep the full list alive: the result has about k capacity.
func TestTopReleasesList(t *testing.T) {
	sims := make([]Neighbor, 1000)
	for i := range sims {
		sims[i] = Neighbor{Index: uint32(i), Sim: float32(i)}
	}
	got := top(sims, 5)
	if len(got) != 5 || cap(got) > 10 || got[0].Index != 999 {
		t.Errorf("top: len %d cap %d first %v", len(got), cap(got), got[0])
	}
}

// insert keeps a full list unchanged for a candidate that is not better.
func TestInsertIntoFullList(t *testing.T) {
	list := []Neighbor{{Index: 1, Sim: 0.9}, {Index: 2, Sim: 0.5}}
	got := insert(list, Neighbor{Index: 3, Sim: 0.1}, 2)
	if len(got) != 2 || got[1].Index != 2 {
		t.Errorf("insert = %v", got)
	}
	got = insert(list, Neighbor{Index: 4, Sim: 0.7}, 2)
	if len(got) != 2 || got[1].Index != 4 {
		t.Errorf("insert better = %v", got)
	}
}
