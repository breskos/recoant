package linalg

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/breskos/recoant/sparse"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestDenseBasics(t *testing.T) {
	m := NewDense(2, 3)
	m.Set(1, 2, 5)
	if m.At(1, 2) != 5 || m.Row(1)[2] != 5 {
		t.Fatal("Set/At/Row")
	}
	tr := m.T()
	if tr.Rows != 3 || tr.At(2, 1) != 5 {
		t.Fatal("T")
	}
	x := []float64{3, 4}
	if Norm(x) != 5 || Dot(x, x) != 25 {
		t.Fatal("Norm/Dot")
	}
	y := []float64{1, 1}
	Axpy(2, x, y)
	Scale(0.5, y)
	if y[0] != 3.5 || y[1] != 4.5 {
		t.Fatalf("Axpy/Scale %v", y)
	}
}

func TestOrthonormalizeRows(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	m := NewDense(4, 10)
	for i := range m.Data {
		m.Data[i] = rng.NormFloat64()
	}
	copy(m.Row(2), m.Row(0)) // dependent row
	if rank := OrthonormalizeRows(m); rank != 3 {
		t.Fatalf("rank %d", rank)
	}
	if Norm(m.Row(2)) != 0 {
		t.Error("dependent row not zero")
	}
	for i := range 4 {
		for j := range 4 {
			want := 0.0
			if i == j && i != 2 {
				want = 1
			}
			if got := Dot(m.Row(i), m.Row(j)); !near(got, want, 1e-12) {
				t.Errorf("q%d·q%d = %v", i, j, got)
			}
		}
	}
}

func TestCholesky(t *testing.T) {
	a := []float64{4, 2, 0.4, 2, 5, 1, 0.4, 1, 3}
	orig := append([]float64(nil), a...)
	if err := Cholesky(a, 3); err != nil {
		t.Fatal(err)
	}
	b := []float64{1, 2, 3}
	x := append([]float64(nil), b...)
	CholeskySolve(a, 3, x)
	for i := range 3 {
		var s float64
		for j := range 3 {
			s += orig[i*3+j] * x[j]
		}
		if !near(s, b[i], 1e-12) {
			t.Errorf("row %d: %v != %v", i, s, b[i])
		}
	}
	if err := Cholesky([]float64{1, 2, 2, 1}, 2); !errors.Is(err, ErrNotPositiveDefinite) {
		t.Errorf("indefinite: %v", err)
	}
}

func TestSymEigen(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	n := 6
	b := NewDense(n, n)
	for i := range b.Data {
		b.Data[i] = rng.NormFloat64()
	}
	a := NewDense(n, n) // a = b·bᵀ, symmetric positive semidefinite
	for i := range n {
		for j := range n {
			a.Set(i, j, Dot(b.Row(i), b.Row(j)))
		}
	}
	vals, vecs := SymEigen(a)
	for k := range n {
		if k > 0 && vals[k] > vals[k-1] {
			t.Fatal("not descending")
		}
		v := vecs.Row(k)
		for i := range n {
			var av float64
			for j := range n {
				av += a.At(i, j) * v[j]
			}
			if !near(av, vals[k]*v[i], 1e-8*math.Max(1, vals[0])) {
				t.Fatalf("eigenpair %d wrong", k)
			}
		}
	}
	id := NewDense(2, 2)
	id.Set(0, 0, 1)
	id.Set(1, 1, 1)
	if vals, _ := SymEigen(id); vals[0] != 1 || vals[1] != 1 {
		t.Errorf("identity: %v", vals)
	}
}

func TestMulRows(t *testing.T) {
	a := sparse.FromRows([][]sparse.Entry{{{Col: 0, Val: 1}, {Col: 2, Val: 2}}, {{Col: 1, Val: 3}}}, 2, 3, 1)
	x := &Dense{Rows: 2, Cols: 3, Data: []float64{1, 1, 1, 0, 1, 2}}
	out := NewDense(2, 2)
	MulRows(a, x, out, 2)
	want := []float64{3, 3, 4, 3}
	for i, w := range want {
		if out.Data[i] != w {
			t.Fatalf("MulRows = %v, want %v", out.Data, want)
		}
	}
	n := 0
	Parallel(0, 0, func(int) { n++ })
	if n != 0 {
		t.Error("Parallel(0)")
	}
}
