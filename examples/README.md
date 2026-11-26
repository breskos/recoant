# Examples

Runnable example programs. They are excluded from the coverage gate in CI.

| Example | Purpose |
| --- | --- |
| [config](config) | Example configurations: a shop (implicit signals, star ratings, returns) and a people-discovery app (likes and passes between users, mutual age preferences, distance filter, retention) |
| [attributes](attributes) | Check attributes, filters, deletion and retention end to end on the attribute data sets of [recoant-data](https://github.com/breskos/recoant-data) (people discovery, shop): replays attribute events and signals, compares filtered with unfiltered recommendations and fails if a deleted reference is recommended, a filter is broken, retention drops the wrong pairs or a sensitive attribute is exported: `go run ./examples/attributes -data ../recoant-data/synthetic` |
| [benchmark](benchmark) | Performance on benchmark/large of recoant-data: ingestion, memory, training, refresh, latency and throughput of every model; `-scale` measures the store at 40 million interactions: `go run ./examples/benchmark -data ../recoant-data/synthetic` |
| [evaluate](evaluate) | Compare all recommenders (ranking metrics, framed by a random floor and an oracle that knows the hidden structure) and the matrix factorizations (rating RMSE) on the synthetic data sets of [recoant-data](https://github.com/breskos/recoant-data): `go run ./examples/evaluate -data ../recoant-data/synthetic` |
