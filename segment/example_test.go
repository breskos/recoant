package segment_test

import (
	"context"
	"fmt"

	"github.com/breskos/recoant/segment"
)

// Two groups of points: k-means puts each group into its own cluster.
func ExampleKMeans() {
	points := [][]float64{{0, 0}, {0, 1}, {1, 0}, {9, 9}, {9, 10}, {10, 9}}
	c, _ := segment.KMeans(context.Background(), points, segment.KMeansConfig{K: 2, Seed: 1})
	fmt.Println(c.Assign[0] == c.Assign[1], c.Assign[1] == c.Assign[2], c.Assign[2] == c.Assign[3])
	fmt.Printf("inertia %.2f\n", c.Inertia)
	// Output:
	// true true false
	// inertia 2.67
}
