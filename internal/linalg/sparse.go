package linalg

import (
	"cmp"
	"runtime"
	"sync"

	"github.com/breskos/recoant/sparse"
)

// MulVec computes y = a·x for a sparse matrix.
func MulVec(a *sparse.CSR, x, y []float64) {
	for i := range a.Rows {
		idx, vals := a.Row(i)
		var s float64
		for k, j := range idx {
			s += float64(vals[k]) * x[j]
		}
		y[i] = s
	}
}

// MulRows computes out[r] = a·x[r] for every row r of x, in parallel.
// out must have x.Rows rows of length a.Rows.
func MulRows(a *sparse.CSR, x, out *Dense, workers int) {
	parallel(x.Rows, workers, func(r int) { MulVec(a, x.Row(r), out.Row(r)) })
}

// parallel calls fn(0..n-1) on up to workers goroutines (default: CPUs).
func parallel(n, workers int, fn func(i int)) {
	workers = min(cmp.Or(workers, runtime.NumCPU()), max(n, 1))
	var wg sync.WaitGroup
	next := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

// Parallel calls fn(0..n-1) on up to workers goroutines (0: CPUs).
func Parallel(n, workers int, fn func(i int)) { parallel(n, workers, fn) }
