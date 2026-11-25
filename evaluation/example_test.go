package evaluation_test

import (
	"fmt"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/evaluation"
)

func ExampleNDCG() {
	recs := []recoant.Ref{p("a"), p("b"), p("c")}
	relevant := evaluation.NewRelevant(p("a"), p("c"))
	fmt.Printf("precision@3 %.2f\n", evaluation.Precision(recs, relevant, 3))
	fmt.Printf("ndcg@3      %.2f\n", evaluation.NDCG(recs, relevant, 3))
	// Output:
	// precision@3 0.67
	// ndcg@3      0.92
}
