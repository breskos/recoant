# recoant

[![Go Reference](https://pkg.go.dev/badge/github.com/breskos/recoant.svg)](https://pkg.go.dev/github.com/breskos/recoant)
[![CI](https://github.com/breskos/recoant/actions/workflows/ci.yml/badge.svg)](https://github.com/breskos/recoant/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/breskos/recoant)](https://goreportcard.com/report/github.com/breskos/recoant)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A recommendation framework in Go: from streams of user signals to recommendations, trained in batch, updated incrementally or learned online.

- User to entity, entity to entity and user to user recommendations
- Signals with depth: visits, clicks, dwell time, purchases and your own types, with weights, value transforms and time decay
- Funnel modeling and learned signal relevance
- Attributes (category, number, set, geo) with filters as data: ranges, distance, and mutual preferences for matching
- Privacy: sensitive attributes, deletion of users and entities, retention per relation
- Relations between users and entities as a typed graph
- Many algorithms behind one interface: popularity, neighborhood models, SVD/PCA, matrix factorization (Funk-SVD, SVD++), ALS, BPR, graph walks and hybrids
- Ranking metrics with temporal splits and prequential evaluation for streams

**What could you build with it?** Seven runnable use cases (online shop, dating, music, recipes, events, job board, customer segments) show how to model an application with recoant, each in one configuration and about 200 lines of Go: [examples/usecases](examples/usecases).

> **Status:** in development. Implemented: signals with implicit, rating and negative feedback, named relations, configuration, popularity, kNN and latent factor models, attributes with filters (including mutual ones), deletion and retention, reciprocal matching with exposure balancing and collective factorization, segments and profiles. Next: streaming. See the [architecture](docs/architecture.md), the [measurements](docs/measurements.md) and the [roadmap](ROADMAP.md).

## Quality and speed

Measured on the synthetic data sets of [recoant-data](https://github.com/breskos/recoant-data) on one 8-core machine (details and meaning in [docs/measurements.md](docs/measurements.md)):

- On users with clear tastes, BPR and ALS reach 94 % and 89 % of the best possible precision (an oracle that knows the hidden structure); on latent-factor data ALS reaches 64 %.
- Attribute filters double the hit rate in people discovery (5.0 % → 12.2 %).
- Reciprocal matching doubles the expected matches in a dating app (0.46 → 1.02 per 10 recommendations) and spreads attention over twice as many profiles.
- Segments on taste vectors recover hidden taste groups exactly (purity 1.0).
- 200,000 users and 50,000 items: ALS trains in 8 s, refreshes after 2,000 new signals in 0.16 s and serves 2,800 requests/s (p99 1.8 ms); kNN serves about 200,000 requests/s.
- The store needs about 20 bytes per interaction: 40 million in 0.73 GB.

## Install

```
go get github.com/breskos/recoant@latest
```

Requires Go 1.24 or newer.

## Packages

| Package | Purpose |
| --- | --- |
| [`recoant`](.) | Core types: `Ref`, `Signal`, `Request`, `Scored`, `Recommender`, `Observer` |
| [`signal`](signal) | Signal model: implicit, rating and negative feedback, weights, transforms, channels, half-life |
| [`store`](store) | Incremental, time-decayed interaction store for millions of users and entities |
| [`sparse`](sparse) | Sparse matrices (CSR) for algorithms |
| [`attr`](attr) | Attribute schemas, an event-fed attribute store and filters as data, including mutual filters for matching |
| [`match`](match) | Reciprocal matching: interest in both directions, combined, with exposure balancing |
| [`segment`](segment) | Segments: k-means on taste vectors and attributes, segment and entity profiles, segments as filters |
| [`config`](config) | Model an application in a JSON configuration: kinds, relations, signals, models, serving |
| [`algo/popularity`](algo/popularity) | Popularity and trending, online |
| [`algo/knn`](algo/knn) | Item-kNN and user-kNN, incremental refresh; similar items and user-to-user |
| [`algo/svd`](algo/svd) | PureSVD and PCA with a randomized truncated SVD |
| [`algo/mf`](algo/mf) | Funk-SVD, biased MF and SVD++ for ratings (Netflix Prize style) |
| [`algo/als`](algo/als) | Alternating least squares for implicit feedback |
| [`algo/bpr`](algo/bpr) | Bayesian personalized ranking |
| [`evaluation`](evaluation) | Precision, recall, NDCG, MAP, hit rate, MRR, coverage; temporal and leave-last-out splits |
| [`persist`](persist) | Save and load stores, attributes and models in JSON or binary |

More packages follow the [roadmap](ROADMAP.md).

## Configure an application

The modeling of an application — kinds, relations, what each signal means, which models run — lives in a JSON configuration (see [examples/config](examples/config) and the [architecture](docs/architecture.md#configuration)):

```go
c, err := config.LoadFile("shop.json")
if err != nil {
	log.Fatal(err)
}
engine, err := c.Build()
if err != nil {
	log.Fatal(err)
}
_ = engine.Add(recoant.Signal{
	Subject: recoant.U("8f3a…"), Object: recoant.Ref{Kind: "product", ID: "p-17"},
	Type: "stars", Value: 2, Time: time.Now(), // below neutral 3: negative feedback
})
_ = engine.Fit(ctx)
recs, err := engine.Recommend(ctx, "for_you", recoant.U("8f3a…"), 10)
```

Attributes arrive as events and restrict candidates with filters from the configuration or per request:

```go
_ = engine.Apply(attr.Event{Ref: recoant.Ref{Kind: "product", ID: "p-17"}, Attribute: "price", Value: attr.Num(49)})
max := 50.0
recs, err = engine.Recommend(ctx, "for_you", recoant.U("8f3a…"), 10,
	recoant.Condition{Attribute: "price", Max: &max})
```

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
