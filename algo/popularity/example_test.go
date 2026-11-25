package popularity_test

import (
	"context"
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/signal"
)

// A trending model with a half-life of one hour prefers what is popular now
// over what was popular yesterday.
func Example() {
	m, err := popularity.New(signal.DefaultModel(), popularity.Config{HalfLife: time.Hour})
	if err != nil {
		panic(err)
	}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	observe := func(article string, n int, at time.Time) {
		for i := range n {
			_ = m.Observe(recoant.Signal{
				Subject: recoant.U(fmt.Sprint("u", i)),
				Object:  recoant.Ref{Kind: "article", ID: article},
				Type:    recoant.Click, Value: 1, Time: at,
			})
		}
	}
	observe("yesterday's news", 50, now.Add(-24*time.Hour))
	observe("breaking", 5, now)

	res, _ := m.Recommend(context.Background(), recoant.Request{Subject: recoant.U("u-1"), Target: "article", K: 2})
	for _, r := range res {
		fmt.Println(r.Ref.ID)
	}
	// Output:
	// breaking
	// yesterday's news
}
