# recoant

[![Go Reference](https://pkg.go.dev/badge/github.com/breskos/recoant.svg)](https://pkg.go.dev/github.com/breskos/recoant)
[![CI](https://github.com/breskos/recoant/actions/workflows/ci.yml/badge.svg)](https://github.com/breskos/recoant/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/breskos/recoant)](https://goreportcard.com/report/github.com/breskos/recoant)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A recommendation framework in Go: from streams of user signals to recommendations, trained in batch, updated incrementally or learned online.

- User to entity, entity to entity and user to user recommendations
- Signals with depth: visits, clicks, dwell time, purchases and your own types, with weights, value transforms and time decay
- Funnel modeling and learned signal relevance
- Relations between users and entities as a typed graph
- Many algorithms behind one interface: popularity, neighborhood models, SVD/PCA, matrix factorization (Funk-SVD, SVD++), ALS, BPR, graph walks and hybrids
- Ranking metrics with temporal splits and prequential evaluation for streams

> **Status:** early design. See the [architecture draft](docs/architecture.md) and the [roadmap](ROADMAP.md).

## Install

```
go get github.com/breskos/recoant@latest
```

Requires Go 1.24 or newer.

## Packages

| Package | Purpose |
| --- | --- |
| [`recoant`](.) | Core types: `Ref`, `Signal`, `Request`, `Scored`, `Recommender`, `Observer` |
| [`signal`](signal) | Signal model: weights, value transforms (e.g. saturating dwell time), half-life |
| [`store`](store) | Incremental, time-decayed interaction store for millions of users and entities |
| [`sparse`](sparse) | Sparse matrices (CSR) for algorithms |
| [`algo/popularity`](algo/popularity) | Popularity and trending, online |
| [`algo/knn`](algo/knn) | Item-kNN and user-kNN, incremental refresh; similar items and user-to-user |
| [`algo/svd`](algo/svd) | PureSVD and PCA with a randomized truncated SVD |
| [`algo/mf`](algo/mf) | Funk-SVD, biased MF and SVD++ for ratings (Netflix Prize style) |
| [`algo/als`](algo/als) | Alternating least squares for implicit feedback |
| [`algo/bpr`](algo/bpr) | Bayesian personalized ranking |
| [`evaluation`](evaluation) | Precision, recall, NDCG, MAP, hit rate, MRR, coverage; temporal and leave-last-out splits |
| [`persist`](persist) | Save and load in JSON or binary |

More packages follow the [roadmap](ROADMAP.md).

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/persist"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

func main() {
	model := signal.DefaultModel() // visit 1, click 2, dwell ≤3, cart 5, purchase 10; half-life 30 days
	s, err := store.New(model)
	if err != nil {
		log.Fatal(err)
	}
	trending, err := popularity.New(model, popularity.Config{HalfLife: 6 * time.Hour})
	if err != nil {
		log.Fatal(err)
	}
	trending.SetSeen(s)

	// Feed signals as they arrive.
	sig := recoant.Signal{
		Subject: recoant.U("8f3a…"), // anonymized user ID
		Object:  recoant.Ref{Kind: "article", ID: "a-1042"},
		Type:    recoant.Dwell, Value: 95, // seconds
		Time:    time.Now(),
	}
	if err := s.Add(sig); err != nil {
		log.Fatal(err)
	}
	if err := trending.Observe(sig); err != nil {
		log.Fatal(err)
	}

	recs, err := trending.Recommend(context.Background(), recoant.Request{
		Subject: recoant.U("c71d…"), Target: "article", K: 10, ExcludeSeen: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(recs)

	if err := persist.SaveStore("store.bin", persist.Binary, s); err != nil {
		log.Fatal(err)
	}
}
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0, see [LICENSE](LICENSE).
