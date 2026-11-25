package linalg

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/breskos/recoant/sparse"
)

// SVDConfig configures [RandomizedSVD].
type SVDConfig struct {
	Rank       int  // number of singular triplets
	Oversample int  // extra random directions, e.g. 10
	PowerIters int  // power iterations, e.g. 2; more is more accurate
	Center     bool // subtract the column means first (PCA)
	Workers    int  // 0: number of CPUs
}

// SVD is a truncated singular value decomposition A ≈ Uᵀ·diag(S)·V, with the
// singular vectors as rows: U is k×m, V is k×n.
type SVD struct {
	U, V *Dense
	S    []float64
	Mean []float64 // column means if centered, else nil
}

// RandomizedSVD computes a truncated SVD of the sparse m×n matrix a with the
// randomized range finder of Halko, Martinsson and Tropp (2011). at must be
// the transpose of a. With Center, it decomposes a minus its column means
// without ever forming the dense centered matrix.
func RandomizedSVD(ctx context.Context, a, at *sparse.CSR, cfg SVDConfig, rng *rand.Rand) (*SVD, error) {
	m, n := a.Rows, a.Cols
	if cfg.Rank <= 0 || cfg.Oversample < 0 || cfg.PowerIters < 0 {
		return nil, fmt.Errorf("linalg: invalid svd config %+v", cfg)
	}
	l := min(cfg.Rank+cfg.Oversample, m, n)
	if l == 0 {
		return nil, fmt.Errorf("linalg: empty matrix %d×%d", m, n)
	}
	var mean []float64
	if cfg.Center {
		mean = a.ColSums()
		Scale(1/float64(m), mean)
	}
	// mulA computes out[r] = A_c·x[r]; mulAT computes out[r] = A_cᵀ·y[r].
	mulA := func(x *Dense) *Dense {
		out := NewDense(x.Rows, m)
		parallel(x.Rows, cfg.Workers, func(r int) {
			y := out.Row(r)
			MulVec(a, x.Row(r), y)
			if mean != nil {
				shift := Dot(mean, x.Row(r))
				for i := range y {
					y[i] -= shift
				}
			}
		})
		return out
	}
	mulAT := func(y *Dense) *Dense {
		out := NewDense(y.Rows, n)
		parallel(y.Rows, cfg.Workers, func(r int) {
			z := out.Row(r)
			MulVec(at, y.Row(r), z)
			if mean != nil {
				var sum float64
				for _, v := range y.Row(r) {
					sum += v
				}
				Axpy(-sum, mean, z)
			}
		})
		return out
	}
	omega := NewDense(l, n)
	for i := range omega.Data {
		omega.Data[i] = rng.NormFloat64()
	}
	q := mulA(omega)
	OrthonormalizeRows(q)
	for range cfg.PowerIters {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		z := mulAT(q)
		OrthonormalizeRows(z)
		q = mulA(z)
		OrthonormalizeRows(q)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := mulAT(q) // l×n, the rows of Qᵀ·A_c
	g := NewDense(l, l)
	for i := range l {
		for j := i; j < l; j++ {
			d := Dot(b.Row(i), b.Row(j))
			g.Set(i, j, d)
			g.Set(j, i, d)
		}
	}
	vals, w := SymEigen(g)
	k := min(cfg.Rank, l)
	out := &SVD{U: NewDense(k, m), V: NewDense(k, n), S: make([]float64, k), Mean: mean}
	for c := range k {
		s := math.Sqrt(math.Max(vals[c], 0))
		out.S[c] = s
		wc := w.Row(c)
		for r := range l {
			Axpy(wc[r], q.Row(r), out.U.Row(c))
			if s > 0 {
				Axpy(wc[r]/s, b.Row(r), out.V.Row(c))
			}
		}
	}
	return out, nil
}
