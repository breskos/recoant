// Package sparse provides sparse matrices for recommendation algorithms.
//
// Interaction data is sparse: a user interacts with a tiny fraction of all
// entities. [CSR] stores only the non-zero values, row by row, with uint32
// column indexes and float32 values (8 bytes per entry), so tens of millions
// of interactions fit comfortably in memory.
package sparse

import (
	"cmp"
	"slices"
)

// Entry is one non-zero value of a row.
type Entry struct {
	Col uint32
	Val float32
}

// Add adds delta to the value at col in a row sorted by column and returns
// the row. A missing entry is inserted at its sorted position.
func Add(row []Entry, col uint32, delta float32) []Entry {
	i, found := slices.BinarySearchFunc(row, col, func(e Entry, c uint32) int { return cmp.Compare(e.Col, c) })
	if found {
		row[i].Val += delta
		return row
	}
	return slices.Insert(row, i, Entry{Col: col, Val: delta})
}

// Get returns the value at col in a row sorted by column. ok is false if the
// row has no entry for col.
func Get(row []Entry, col uint32) (v float32, ok bool) {
	i, found := slices.BinarySearchFunc(row, col, func(e Entry, c uint32) int { return cmp.Compare(e.Col, c) })
	if !found {
		return 0, false
	}
	return row[i].Val, true
}

// CSR is a matrix in compressed sparse row format. The column indexes of row
// i are Indices[Indptr[i]:Indptr[i+1]], sorted ascending, with the values at
// the same positions in Values.
type CSR struct {
	Rows, Cols int
	Indptr     []int
	Indices    []uint32
	Values     []float32
}

// FromRows builds a matrix with rows rows and cols columns. rows may be
// shorter than the number of rows; missing rows are empty. Each row must be
// sorted by column. Values are multiplied by scale.
func FromRows(entries [][]Entry, rows, cols int, scale float32) *CSR {
	nnz := 0
	for _, r := range entries {
		nnz += len(r)
	}
	m := &CSR{
		Rows:    rows,
		Cols:    cols,
		Indptr:  make([]int, rows+1),
		Indices: make([]uint32, 0, nnz),
		Values:  make([]float32, 0, nnz),
	}
	for i := range rows {
		if i < len(entries) {
			for _, e := range entries[i] {
				m.Indices = append(m.Indices, e.Col)
				m.Values = append(m.Values, e.Val*scale)
			}
		}
		m.Indptr[i+1] = len(m.Indices)
	}
	return m
}

// NNZ returns the number of stored values.
func (m *CSR) NNZ() int { return len(m.Indices) }

// Row returns the column indexes and values of row i. The slices share
// memory with the matrix.
func (m *CSR) Row(i int) ([]uint32, []float32) {
	a, b := m.Indptr[i], m.Indptr[i+1]
	return m.Indices[a:b], m.Values[a:b]
}

// At returns the value at row i and column j, or 0.
func (m *CSR) At(i int, j uint32) float32 {
	idx, vals := m.Row(i)
	k, found := slices.BinarySearch(idx, j)
	if !found {
		return 0
	}
	return vals[k]
}

// Transpose returns the transposed matrix, which is the CSC form of m.
func (m *CSR) Transpose() *CSR {
	t := &CSR{
		Rows:    m.Cols,
		Cols:    m.Rows,
		Indptr:  make([]int, m.Cols+1),
		Indices: make([]uint32, m.NNZ()),
		Values:  make([]float32, m.NNZ()),
	}
	for _, j := range m.Indices {
		t.Indptr[j+1]++
	}
	for j := range m.Cols {
		t.Indptr[j+1] += t.Indptr[j]
	}
	next := slices.Clone(t.Indptr[:m.Cols])
	for i := range m.Rows {
		idx, vals := m.Row(i)
		for k, j := range idx {
			p := next[j]
			t.Indices[p] = uint32(i)
			t.Values[p] = vals[k]
			next[j]++
		}
	}
	return t
}

// RowSums returns the sum of each row.
func (m *CSR) RowSums() []float64 {
	s := make([]float64, m.Rows)
	for i := range m.Rows {
		_, vals := m.Row(i)
		for _, v := range vals {
			s[i] += float64(v)
		}
	}
	return s
}

// ColSums returns the sum of each column.
func (m *CSR) ColSums() []float64 {
	s := make([]float64, m.Cols)
	for k, j := range m.Indices {
		s[j] += float64(m.Values[k])
	}
	return s
}

// MulVec returns m · x. x must have Cols elements.
func (m *CSR) MulVec(x []float64) []float64 {
	y := make([]float64, m.Rows)
	for i := range m.Rows {
		idx, vals := m.Row(i)
		var sum float64
		for k, j := range idx {
			sum += float64(vals[k]) * x[j]
		}
		y[i] = sum
	}
	return y
}
