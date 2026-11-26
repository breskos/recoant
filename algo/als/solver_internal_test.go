package als

import (
	"testing"

	"github.com/breskos/recoant/internal/embed"
)

// A system that is not positive definite yields a neutral vector instead
// of garbage. With a positive regularization this cannot happen in
// training; the guard keeps the model safe if it ever does.
func TestSolveNotPositiveDefinite(t *testing.T) {
	s := newSolver(2, -1, 1, embed.New(1, 2), make([]float64, 4))
	out := []float64{7, 7}
	s.solve(nil, nil, out)
	if out[0] != 0 || out[1] != 0 {
		t.Errorf("out = %v, want zeros", out)
	}
}
