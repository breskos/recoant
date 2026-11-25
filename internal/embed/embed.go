// Package embed holds dense factor matrices (one vector per user or item)
// and scores them: dot products for recommendations, cosine for similar
// rows.
package embed

import (
	"math"
	"math/rand/v2"

	"github.com/breskos/recoant/internal/topk"
)

// Matrix holds N vectors of length K, row-major.
type Matrix struct {
	N, K int
	Data []float64
}

// New returns a zero matrix.
func New(n, k int) *Matrix { return &Matrix{N: n, K: k, Data: make([]float64, n*k)} }

// Random returns a matrix with entries drawn from N(0, std²).
func Random(rng *rand.Rand, n, k int, std float64) *Matrix {
	m := New(n, k)
	for i := range m.Data {
		m.Data[i] = rng.NormFloat64() * std
	}
	return m
}

// Row returns vector i, sharing memory with the matrix.
func (m *Matrix) Row(i int) []float64 { return m.Data[i*m.K : (i+1)*m.K] }

// Grow appends random rows until the matrix has n rows.
func (m *Matrix) Grow(n int, rng *rand.Rand, std float64) {
	for m.N < n {
		for range m.K {
			m.Data = append(m.Data, rng.NormFloat64()*std)
		}
		m.N++
	}
}

// Norms returns the Euclidean norm of every row.
func (m *Matrix) Norms() []float64 {
	out := make([]float64, m.N)
	for i := range m.N {
		r := m.Row(i)
		var s float64
		for _, x := range r {
			s += x * x
		}
		out[i] = math.Sqrt(s)
	}
	return out
}

// Score offers v·row_j (+ bias[j] if bias is not nil) for every row j that
// admit accepts.
func (m *Matrix) Score(v, bias []float64, admit func(uint32) bool, tk *topk.TopK) {
	for j := range m.N {
		if !admit(uint32(j)) {
			continue
		}
		r := m.Row(j)
		var s float64
		for c, x := range v {
			s += x * r[c]
		}
		if bias != nil {
			s += bias[j]
		}
		tk.Push(uint32(j), s)
	}
}

// Similar offers the cosine similarity between v (with norm vn) and every
// row j that admit accepts. Rows with zero norm are skipped.
func (m *Matrix) Similar(v []float64, vn float64, norms []float64, admit func(uint32) bool, tk *topk.TopK) {
	if vn == 0 {
		return
	}
	for j := range m.N {
		if norms[j] == 0 || !admit(uint32(j)) {
			continue
		}
		r := m.Row(j)
		var s float64
		for c, x := range v {
			s += x * r[c]
		}
		tk.Push(uint32(j), s/(vn*norms[j]))
	}
}
