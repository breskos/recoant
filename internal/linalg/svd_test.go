package linalg

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/breskos/recoant/sparse"
)

// lowRank returns a sparse m×n matrix u·vᵀ of the given rank with about
// density of its entries kept.
func lowRank(rng *rand.Rand, m, n, rank int, density float64) *sparse.CSR {
	u, v := NewDense(m, rank), NewDense(n, rank)
	for i := range u.Data {
		u.Data[i] = rng.NormFloat64()
	}
	for i := range v.Data {
		v.Data[i] = rng.NormFloat64()
	}
	rows := make([][]sparse.Entry, m)
	for i := range m {
		for j := range n {
			if rng.Float64() < density {
				rows[i] = append(rows[i], sparse.Entry{Col: uint32(j), Val: float32(Dot(u.Row(i), v.Row(j)))})
			}
		}
	}
	return sparse.FromRows(rows, m, n, 1)
}

// exactSingularValues returns the singular values of a (with optional
// column centering) from the eigenvalues of AᵀA.
func exactSingularValues(a *sparse.CSR, center bool) []float64 {
	d := NewDense(a.Rows, a.Cols)
	for i := range a.Rows {
		idx, vals := a.Row(i)
		for k, j := range idx {
			d.Set(i, int(j), float64(vals[k]))
		}
	}
	if center {
		for j := range a.Cols {
			var s float64
			for i := range a.Rows {
				s += d.At(i, j)
			}
			for i := range a.Rows {
				d.Set(i, j, d.At(i, j)-s/float64(a.Rows))
			}
		}
	}
	dt := d.T()
	g := NewDense(a.Cols, a.Cols)
	for i := range a.Cols {
		for j := range a.Cols {
			g.Set(i, j, Dot(dt.Row(i), dt.Row(j)))
		}
	}
	vals, _ := SymEigen(g)
	for i, v := range vals {
		vals[i] = math.Sqrt(math.Max(v, 0))
	}
	return vals
}

func TestRandomizedSVD(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	a := lowRank(rng, 120, 40, 4, 1)
	for _, center := range []bool{false, true} {
		svd, err := RandomizedSVD(context.Background(), a, a.Transpose(), SVDConfig{Rank: 6, Oversample: 10, PowerIters: 2, Center: center, Workers: 2}, rng)
		if err != nil {
			t.Fatal(err)
		}
		exact := exactSingularValues(a, center)
		for c := range 6 {
			if math.Abs(svd.S[c]-exact[c]) > 1e-6*exact[0] {
				t.Errorf("center=%v σ%d = %v, want %v", center, c, svd.S[c], exact[c])
			}
		}
		// Singular vectors are orthonormal and satisfy A_c·v = σ·u.
		for c := range 4 {
			if math.Abs(Norm(svd.U.Row(c))-1) > 1e-8 || math.Abs(Norm(svd.V.Row(c))-1) > 1e-8 {
				t.Errorf("center=%v vector %d not unit", center, c)
			}
			av := make([]float64, a.Rows)
			MulVec(a, svd.V.Row(c), av)
			if center {
				shift := Dot(svd.Mean, svd.V.Row(c))
				for i := range av {
					av[i] -= shift
				}
			}
			for i := range av {
				if math.Abs(av[i]-svd.S[c]*svd.U.Row(c)[i]) > 1e-6*svd.S[0] {
					t.Fatalf("center=%v A·v ≠ σ·u for %d", center, c)
				}
			}
		}
	}
}

func TestRandomizedSVDErrors(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	a := lowRank(rng, 10, 5, 2, 1)
	ctx := context.Background()
	if _, err := RandomizedSVD(ctx, a, a.Transpose(), SVDConfig{}, rng); err == nil {
		t.Error("rank 0 accepted")
	}
	empty := sparse.FromRows(nil, 0, 0, 1)
	if _, err := RandomizedSVD(ctx, empty, empty, SVDConfig{Rank: 2}, rng); err == nil {
		t.Error("empty matrix accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := RandomizedSVD(canceled, a, a.Transpose(), SVDConfig{Rank: 2, PowerIters: 1}, rng); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	if _, err := RandomizedSVD(canceled, a, a.Transpose(), SVDConfig{Rank: 2}, rng); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled without power iterations: %v", err)
	}
}
