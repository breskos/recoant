# Change Log
All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
- Core types in `recoant`: `Ref`, `Kind`, `Signal`, `SignalType`, `Request`, `Scored` and the `Recommender`, `Observer` and `Seen` interfaces.
- `signal`: signal model with weights per signal type, value transforms (constant, linear, log, saturate, threshold, cap) and a half-life.
- `store`: incremental interaction store with interned IDs, sorted sparse rows and forward decay; snapshots of relations as CSR matrices. Measured: 40 million interactions of 2 million users with 500,000 items in 0.73 GB heap.
- `sparse`: CSR matrices with transpose, row and column sums and matrix-vector product.
- `algo/popularity`: all-time popularity and trending (half-life), online and batch.
- `evaluation`: precision, recall, NDCG, MAP, hit rate and MRR at k, coverage; splits by time, by fraction and leave-last-out; parallel evaluation.
- `persist`: save and load stores, signal models and popularity models in JSON or binary, detected on load, written atomically.
- Project setup: module, CI (vet, race tests, coverage gate, golangci-lint), contribution guidelines and templates.
