package segment

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
)

// ErrConfig is returned for invalid configurations.
var ErrConfig = errors.New("segment: invalid configuration")

// KMeansConfig configures k-means.
type KMeansConfig struct {
	// K is the number of clusters.
	K int `json:"k"`
	// Iterations bounds the Lloyd iterations (or mini-batch steps);
	// default 50 (k-means) or enough steps to see every point 20 times,
	// at least 100 (mini-batch).
	Iterations int `json:"iterations,omitempty"`
	// BatchSize switches to mini-batch k-means (Sculley, 2010) with this
	// many points per step; 0 runs full k-means. Mini-batches suit large
	// and growing sets.
	BatchSize int `json:"batch_size,omitempty"`
	// Seed makes the result reproducible.
	Seed uint64 `json:"seed,omitempty"`
	// Restarts is the number of runs from different seeds; the run with the
	// lowest inertia wins. Default 10.
	Restarts int `json:"restarts,omitempty"`
}

// Validate reports an error for a configuration out of range.
func (c KMeansConfig) Validate() error {
	if c.K < 1 || c.Iterations < 0 || c.BatchSize < 0 || c.Restarts < 0 {
		return fmt.Errorf("%w: k must be positive, iterations, batch_size and restarts not negative, got %+v", ErrConfig, c)
	}
	return nil
}

// Clusters is the result of k-means.
type Clusters struct {
	Centers [][]float64
	// Assign holds the cluster of every point.
	Assign []int
	// Inertia is the sum of squared distances of the points to their
	// centers.
	Inertia float64
}

// KMeans clusters points (all of the same length) into at most cfg.K
// clusters, starting from k-means++ seeds. With fewer distinct points than
// K, the result has fewer clusters.
func KMeans(ctx context.Context, points [][]float64, cfg KMeansConfig) (Clusters, error) {
	if err := cfg.Validate(); err != nil {
		return Clusters{}, err
	}
	if len(points) == 0 {
		return Clusters{}, nil
	}
	var best Clusters
	for r := range uint64(cmp.Or(cfg.Restarts, 10)) {
		seed := cfg.Seed + r*0x9e3779b97f4a7c15
		c, err := run(ctx, rand.New(rand.NewPCG(seed, seed^0x5851f42d4c957f2d)), points, cfg)
		if err != nil {
			return Clusters{}, err
		}
		if r == 0 || c.Inertia < best.Inertia {
			best = c
		}
	}
	return best, nil
}

// run is one k-means run.
func run(ctx context.Context, rng *rand.Rand, points [][]float64, cfg KMeansConfig) (Clusters, error) {
	centers := seed(rng, points, cfg.K)
	var err error
	if cfg.BatchSize > 0 {
		steps := cmp.Or(cfg.Iterations, max(100, 20*len(points)/cfg.BatchSize))
		err = miniBatch(ctx, rng, points, centers, steps, cfg.BatchSize)
	} else {
		err = lloyd(ctx, points, centers, cmp.Or(cfg.Iterations, 50))
	}
	if err != nil {
		return Clusters{}, err
	}
	c := Clusters{Centers: centers, Assign: make([]int, len(points))}
	for i, p := range points {
		j, d := nearest(centers, p)
		c.Assign[i], c.Inertia = j, c.Inertia+d
	}
	return c, nil
}

// seed picks k-means++ centers: the first at random, each next one with a
// probability proportional to its squared distance to the nearest center.
func seed(rng *rand.Rand, points [][]float64, k int) [][]float64 {
	centers := [][]float64{clone(points[rng.IntN(len(points))])}
	dist := make([]float64, len(points))
	for i, p := range points {
		dist[i] = sqDist(p, centers[0])
	}
	for len(centers) < k {
		sum := 0.0
		for _, d := range dist {
			sum += d
		}
		if sum == 0 { // fewer distinct points than k
			break
		}
		x, next := rng.Float64()*sum, len(points)-1
		for i, d := range dist {
			if x -= d; x < 0 {
				next = i
				break
			}
		}
		c := clone(points[next])
		centers = append(centers, c)
		for i, p := range points {
			dist[i] = min(dist[i], sqDist(p, c))
		}
	}
	return centers
}

// lloyd alternates assignment and update until no point changes its
// cluster. An empty cluster is moved to the point farthest from its
// center.
func lloyd(ctx context.Context, points, centers [][]float64, iterations int) error {
	assign := make([]int, len(points))
	for i := range assign {
		assign[i] = -1
	}
	dim := len(points[0])
	for range iterations {
		if err := ctx.Err(); err != nil {
			return err
		}
		changed, far, farDist := false, 0, -1.0
		for i, p := range points {
			j, d := nearest(centers, p)
			if j != assign[i] {
				assign[i], changed = j, true
			}
			if d > farDist {
				far, farDist = i, d
			}
		}
		if !changed {
			return nil
		}
		sums, counts := make([][]float64, len(centers)), make([]int, len(centers))
		for j := range sums {
			sums[j] = make([]float64, dim)
		}
		for i, p := range points {
			add(sums[assign[i]], p, 1)
			counts[assign[i]]++
		}
		for j := range centers {
			if counts[j] == 0 {
				copy(centers[j], points[far])
				continue
			}
			for d := range centers[j] {
				centers[j][d] = sums[j][d] / float64(counts[j])
			}
		}
	}
	return nil
}

// miniBatch moves the nearest center of each sampled point towards it with
// a per-center learning rate of 1/count.
func miniBatch(ctx context.Context, rng *rand.Rand, points, centers [][]float64, steps, size int) error {
	counts := make([]int, len(centers))
	batch := make([]int, size)
	for range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		for b := range batch {
			batch[b] = rng.IntN(len(points))
		}
		for _, i := range batch {
			j, _ := nearest(centers, points[i])
			counts[j]++
			eta := 1 / float64(counts[j])
			for d, x := range points[i] {
				centers[j][d] += eta * (x - centers[j][d])
			}
		}
	}
	return nil
}

func nearest(centers [][]float64, p []float64) (int, float64) {
	best, bestD := 0, math.Inf(1)
	for j, c := range centers {
		if d := sqDist(p, c); d < bestD {
			best, bestD = j, d
		}
	}
	return best, bestD
}

func sqDist(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

func add(dst, x []float64, w float64) {
	for i := range x {
		dst[i] += w * x[i]
	}
}

func clone(x []float64) []float64 { return append([]float64(nil), x...) }
