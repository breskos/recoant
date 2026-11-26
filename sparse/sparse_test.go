package sparse_test

import (
	"slices"
	"testing"

	"github.com/breskos/recoant/sparse"
)

func TestAddGet(t *testing.T) {
	var row []sparse.Entry
	for _, c := range []uint32{5, 1, 3, 1} {
		row = sparse.Add(row, c, 1)
	}
	want := []sparse.Entry{{1, 2}, {3, 1}, {5, 1}}
	if !slices.Equal(row, want) {
		t.Fatalf("row = %v, want %v", row, want)
	}
	if v, ok := sparse.Get(row, 1); !ok || v != 2 {
		t.Error("Get")
	}
	if _, ok := sparse.Get(row, 4); ok {
		t.Error("Get")
	}
}

func TestSetMax(t *testing.T) {
	var row []sparse.Entry
	row = sparse.Set(row, 4, 2)
	row = sparse.Set(row, 4, 1)
	row = sparse.Max(row, 4, 0.5)
	row = sparse.Max(row, 2, 3)
	row = sparse.Max(row, 2, 5)
	want := []sparse.Entry{{2, 5}, {4, 1}}
	if !slices.Equal(row, want) {
		t.Fatalf("row = %v, want %v", row, want)
	}
}

func TestCSR(t *testing.T) {
	// 3×4 matrix:
	// [1 0 2 0]
	// [0 0 0 0]
	// [0 3 0 4]
	rows := [][]sparse.Entry{{{0, 1}, {2, 2}}, nil, {{1, 3}, {3, 4}}}
	m := sparse.FromRows(rows[:1], 3, 4, 1) // shorter input: missing rows empty
	if m.NNZ() != 2 || m.Rows != 3 {
		t.Fatalf("short rows: %+v", m)
	}
	m = sparse.FromRows(rows, 3, 4, 2)
	if m.NNZ() != 4 || m.At(2, 3) != 8 || m.At(1, 1) != 0 || m.At(0, 1) != 0 {
		t.Fatalf("FromRows: %+v", m)
	}
	m = sparse.FromRows(rows, 3, 4, 1)
	if got := m.RowSums(); !slices.Equal(got, []float64{3, 0, 7}) {
		t.Errorf("RowSums = %v", got)
	}
	if got := m.ColSums(); !slices.Equal(got, []float64{1, 3, 2, 4}) {
		t.Errorf("ColSums = %v", got)
	}
	if got := m.MulVec([]float64{1, 1, 1, 1}); !slices.Equal(got, []float64{3, 0, 7}) {
		t.Errorf("MulVec = %v", got)
	}
	tr := m.Transpose()
	if tr.Rows != 4 || tr.Cols != 3 {
		t.Fatalf("Transpose shape %d×%d", tr.Rows, tr.Cols)
	}
	for i := range 3 {
		for j := range 4 {
			if m.At(i, uint32(j)) != tr.At(j, uint32(i)) {
				t.Fatalf("Transpose differs at %d,%d", i, j)
			}
		}
	}
	idx, _ := tr.Row(3)
	if !slices.Equal(idx, []uint32{2}) {
		t.Errorf("Transpose row 3 = %v", idx)
	}
}

func BenchmarkAdd(b *testing.B) {
	var row []sparse.Entry
	for i := range b.N {
		row = sparse.Add(row, uint32(i*7919%1000), 1)
	}
}
