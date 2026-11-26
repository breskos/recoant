# Examples

Runnable example programs. They are excluded from the coverage gate in CI.

| Example | Purpose |
| --- | --- |
| [usecases](usecases) | **Start here.** Six runnable use cases with a story each: online shop, dating, music, recipes, events, job board. Every one shows how to model an application with recoant and which design decisions to copy: `go run ./examples/usecases/shop` |
| [config](config) | Example configurations: a shop (implicit signals, star ratings, returns) and a people-discovery app (likes and passes between users, mutual age preferences, distance filter, retention) |
| [matching](matching) | Measure reciprocal matching on the dating data set of [recoant-data](https://github.com/breskos/recoant-data): one-way ALS against reciprocal matching with and without exposure balancing, framed by a random floor and an oracle; reports expected and logged matches and how concentrated recommendations are: `go run ./examples/matching -data ../recoant-data/synthetic` |
| [attributes](attributes) | Check attributes, filters, deletion and retention end to end on the attribute data sets of [recoant-data](https://github.com/breskos/recoant-data) (people discovery, shop): replays attribute events and signals, compares filtered with unfiltered recommendations and fails if a deleted reference is recommended, a filter is broken, retention drops the wrong pairs or a sensitive attribute is exported: `go run ./examples/attributes -data ../recoant-data/synthetic` |
| [benchmark](benchmark) | Performance on benchmark/large of recoant-data: ingestion, memory, training, refresh, latency and throughput of every model; `-scale` measures the store at 40 million interactions: `go run ./examples/benchmark -data ../recoant-data/synthetic` |
| [evaluate](evaluate) | Compare all recommenders (ranking metrics, framed by a random floor and an oracle that knows the hidden structure) and the matrix factorizations (rating RMSE) on the synthetic data sets of [recoant-data](https://github.com/breskos/recoant-data): `go run ./examples/evaluate -data ../recoant-data/synthetic` |
