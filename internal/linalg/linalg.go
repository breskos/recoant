// Package linalg provides the dense linear algebra recoant needs, built on
// the standard library: vector operations, Gram-Schmidt orthonormalization,
// Cholesky factorization, symmetric eigendecomposition and sparse–dense
// products. Sets of vectors are stored as rows of a [Dense] matrix, so that
// every vector is contiguous in memory.
package linalg

import (
	"errors"
	"math"
	"sort"
)

// ErrNotPositiveDefinite is returned by [Cholesky].
var ErrNotPositiveDefinite = errors.New("linalg: matrix is not positive definite")

// Dense is a row-major matrix.
type Dense struct {
	Rows, Cols int
	Data       []float64
}

// NewDense returns a zero matrix.
func NewDense(rows, cols int) *Dense {
	return &Dense{Rows: rows, Cols: cols, Data: make([]float64, rows*cols)}
}

// Row returns row i, sharing memory with the matrix.
func (m *Dense) Row(i int) []float64 { return m.Data[i*m.Cols : (i+1)*m.Cols] }

// At returns the element at row i and column j.
func (m *Dense) At(i, j int) float64 { return m.Data[i*m.Cols+j] }

// Set sets the element at row i and column j.
func (m *Dense) Set(i, j int, v float64) { m.Data[i*m.Cols+j] = v }

// T returns the transpose.
func (m *Dense) T() *Dense {
	t := NewDense(m.Cols, m.Rows)
	for i := range m.Rows {
		for j := range m.Cols {
			t.Data[j*m.Rows+i] = m.Data[i*m.Cols+j]
		}
	}
	return t
}

// Dot returns a·b.
func Dot(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Axpy computes y += alpha·x.
func Axpy(alpha float64, x, y []float64) {
	for i := range x {
		y[i] += alpha * x[i]
	}
}

// Scale computes x *= alpha.
func Scale(alpha float64, x []float64) {
	for i := range x {
		x[i] *= alpha
	}
}

// Norm returns the Euclidean norm of x.
func Norm(x []float64) float64 { return math.Sqrt(Dot(x, x)) }

// OrthonormalizeRows makes the rows of m orthonormal with modified
// Gram-Schmidt, applied twice for numerical stability. Rows that are
// (numerically) linearly dependent on earlier rows become zero. It returns
// the number of non-zero rows.
func OrthonormalizeRows(m *Dense) int {
	rank := 0
	for i := range m.Rows {
		r := m.Row(i)
		before := Norm(r)
		for range 2 {
			for j := range i {
				q := m.Row(j)
				Axpy(-Dot(q, r), q, r)
			}
		}
		n := Norm(r)
		if n <= 1e-10*math.Max(before, 1e-300) || n == 0 {
			clear(r)
			continue
		}
		Scale(1/n, r)
		rank++
	}
	return rank
}

// Cholesky factorizes the symmetric positive definite n×n matrix a (row
// major) in place into its lower triangular factor L with a = L·Lᵀ. The
// upper triangle is left untouched.
func Cholesky(a []float64, n int) error {
	for j := range n {
		d := a[j*n+j]
		for k := range j {
			d -= a[j*n+k] * a[j*n+k]
		}
		if d <= 0 || math.IsNaN(d) {
			return ErrNotPositiveDefinite
		}
		d = math.Sqrt(d)
		a[j*n+j] = d
		for i := j + 1; i < n; i++ {
			s := a[i*n+j]
			for k := range j {
				s -= a[i*n+k] * a[j*n+k]
			}
			a[i*n+j] = s / d
		}
	}
	return nil
}

// CholeskySolve solves L·Lᵀ·x = b in place (b becomes x), with L from
// [Cholesky].
func CholeskySolve(l []float64, n int, b []float64) {
	for i := range n {
		s := b[i]
		for k := range i {
			s -= l[i*n+k] * b[k]
		}
		b[i] = s / l[i*n+i]
	}
	for i := n - 1; i >= 0; i-- {
		s := b[i]
		for k := i + 1; k < n; k++ {
			s -= l[k*n+i] * b[k]
		}
		b[i] = s / l[i*n+i]
	}
}

// SymEigen returns the eigenvalues of the symmetric matrix a in descending
// order and the eigenvectors as the rows of vecs (cyclic Jacobi method). a
// is not modified. It is meant for small matrices (up to a few hundred
// rows).
func SymEigen(a *Dense) (vals []float64, vecs *Dense) {
	n := a.Rows
	m := &Dense{Rows: n, Cols: n, Data: append([]float64(nil), a.Data...)}
	v := NewDense(n, n) // columns are eigenvectors
	for i := range n {
		v.Set(i, i, 1)
	}
	for sweep := 0; sweep < 100; sweep++ {
		var off float64
		for p := range n {
			for q := p + 1; q < n; q++ {
				off += m.At(p, q) * m.At(p, q)
			}
		}
		if off < 1e-22 {
			break
		}
		for p := range n {
			for q := p + 1; q < n; q++ {
				apq := m.At(p, q)
				if math.Abs(apq) < 1e-300 {
					continue
				}
				theta := (m.At(q, q) - m.At(p, p)) / (2 * apq)
				t := math.Copysign(1, theta) / (math.Abs(theta) + math.Sqrt(theta*theta+1))
				c := 1 / math.Sqrt(t*t+1)
				s := t * c
				for k := range n {
					akp, akq := m.At(k, p), m.At(k, q)
					m.Set(k, p, c*akp-s*akq)
					m.Set(k, q, s*akp+c*akq)
				}
				for k := range n {
					apk, aqk := m.At(p, k), m.At(q, k)
					m.Set(p, k, c*apk-s*aqk)
					m.Set(q, k, s*apk+c*aqk)
				}
				for k := range n {
					vkp, vkq := v.At(k, p), v.At(k, q)
					v.Set(k, p, c*vkp-s*vkq)
					v.Set(k, q, s*vkp+c*vkq)
				}
			}
		}
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return m.At(order[i], order[i]) > m.At(order[j], order[j]) })
	vals = make([]float64, n)
	vecs = NewDense(n, n)
	for r, i := range order {
		vals[r] = m.At(i, i)
		for k := range n {
			vecs.Set(r, k, v.At(k, i))
		}
	}
	return vals, vecs
}
