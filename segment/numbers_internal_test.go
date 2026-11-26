package segment

import (
	"slices"
	"testing"
)

func TestKeepNumbers(t *testing.T) {
	old := [][]float64{{0}, {10}, {20}}
	// The new run found the same clusters in another order.
	f := &fitted{centers: [][]float64{{20.5}, {0.5}, {9.5}}, assign: []int{0, 1, 2, 1}}
	keepNumbers(old, f)
	if !slices.Equal(f.assign, []int{2, 0, 1, 0}) || f.centers[0][0] != 0.5 || f.centers[2][0] != 20.5 {
		t.Errorf("renumbered: %v %v", f.assign, f.centers)
	}
	// Fewer new segments than old ones, and more.
	f = &fitted{centers: [][]float64{{19}}, assign: []int{0}}
	keepNumbers(old, f)
	if f.assign[0] != 0 {
		t.Errorf("one segment: %v", f.assign)
	}
	f = &fitted{centers: [][]float64{{0}, {10}, {20}, {30}}, assign: []int{3, 2}}
	keepNumbers(old, f)
	if !slices.Equal(f.assign, []int{3, 2}) {
		t.Errorf("one more segment: %v", f.assign)
	}
	// Other dimensions: unchanged.
	f = &fitted{centers: [][]float64{{1, 2}}, assign: []int{0}}
	keepNumbers(old, f)
	if f.assign[0] != 0 {
		t.Errorf("dimensions: %v", f.assign)
	}
}
