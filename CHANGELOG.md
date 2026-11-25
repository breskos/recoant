# Change Log
All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- `algo/knn`: item-kNN and user-kNN with cosine, Jaccard and adjusted cosine similarity, shrinkage, a limit for very active rows, and parallel training. Directions: user→item, item→item (similar items), user→item via similar users and user→user (similar users). `Refresh` updates only the rows that changed in the store since the last training. Measured on recoant-data benchmark/large (200,000 users, 50,000 items, 1.5 million interactions, 8 CPUs): item-kNN fits in 0.15 s and refreshes after 2,000 new signals in 0.06 s; user-kNN fits in 9.7 s and refreshes in 7.7 s, or 6 s and 0.8 s with `MaxDegree: 1000`.
- `store`: `Seq` and `Changes` tell which subjects and objects changed since a point in time, `Row` returns a subject's interactions by dense index.
- `examples/evaluate`: compares popularity, item-kNN and user-kNN on the recoant-data sets.
- Core types in `recoant`: `Ref`, `Kind`, `Signal`, `SignalType`, `Request`, `Scored` and the `Recommender`, `Observer` and `Seen` interfaces.
- `signal`: signal model with weights per signal type, value transforms (constant, linear, log, saturate, threshold, cap) and a half-life.
- `store`: incremental interaction store with interned IDs, sorted sparse rows and forward decay; snapshots of relations as CSR matrices. Measured: 40 million interactions of 2 million users with 500,000 items in 0.73 GB heap.
- `sparse`: CSR matrices with transpose, row and column sums and matrix-vector product.
- `algo/popularity`: all-time popularity and trending (half-life), online and batch.
- `evaluation`: precision, recall, NDCG, MAP, hit rate and MRR at k, coverage; splits by time, by fraction and leave-last-out; parallel evaluation.
- `persist`: save and load stores, signal models and popularity models in JSON or binary, detected on load, written atomically.
- Project setup: module, CI (vet, race tests, coverage gate, golangci-lint), contribution guidelines and templates.
