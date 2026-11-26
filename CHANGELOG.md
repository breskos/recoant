# Change Log
All notable changes to this project will be documented in this file.

## [Unreleased]

### Added (v0.4: signals 2.0 and configuration)
- **Feedback kinds** in the signal model: `signal.Implicit` (default), `signal.Rating` with `Scale` and `Neutral` (a 1-star rating on a 1–5 scale with neutral 3 counts negative) and `signal.Negative` (dislike, pass, return, block).
- **Channels** per relation with their own aggregation — `signal.Sum`, `signal.Last` (a re-rating replaces the old rating), `signal.Max`, `signal.Mean` — and half-life. Defaults: `positive`, `negative` and `rating`. `store.ChannelMatrix` returns the raw values of a channel, `store.Channels` lists them.
- **Named relations**: `Rule.Relation`, `store.Relation.Name`, `signal.RelationSpec` with kinds, half-life and channel settings per relation. Signals for a declared relation must connect its kinds.
- **Preference**: `store.Matrix`, `Row`, `Strength` and `Interactions` return the signed preference, the sum over channels of weight·(value − neutral).
- **Negative feedback in the models**: rejected objects are never recommended unless `recoant.Request.IncludeRejected` is set (`store.Rejected`, `recoant.Rejecter`); popularity counts negative signals down; Jaccard ignores negative interactions; cosine kNN uses signed strengths; ALS treats negative preference as confident zeros; BPR samples rejected objects as hard negatives; `mf.Config.Channel` trains on a channel such as the ratings.
- **`config` package**: versioned JSON configuration with kinds (and attribute schemas, used from v0.5), relations, channels, signals, models and serving; strict validation with field paths in errors; `Config.Build` creates an `Engine` with store and models, `Engine.Recommend` answers a serving declaration with fallback. Durations as strings, including days (`"30d"`).
- Example configurations: `examples/config/shop.json`, `examples/config/people.json`.

### Changed (v0.4)
- `signal.Model.Weight` returns the signed contribution (negative for negative feedback, centered for ratings). `Strength` uses the decay of the signal's channel.
- `signal.Model.ChannelNames` and `ChannelSpec` take the relation name.
- Store snapshots are version 2 (channels per relation); version 1 snapshots still load as one `positive` channel.

### Migration (v0.4)
- `store.Relation` has a `Name` field. Code that builds `store.Relation{Subject: …, Object: …}` keeps working for signals whose rule names no relation.
- Code that relied on `Weight` being non-negative must handle negative values.
- Recommenders now exclude rejected objects by default; set `IncludeRejected` to get the previous behavior.


### Added
- `algo/svd`: PureSVD with a randomized truncated SVD (Halko, Martinsson and Tropp) that never forms a dense matrix; optional centering turns it into a PCA. Subjects are folded in from their live history.
- `algo/mf`: matrix factorization for explicit feedback trained with SGD: Funk-SVD, biased MF and SVD++. `Predict` for rating prediction; SVD++ folds in subjects that were not part of training.
- `algo/als`: alternating least squares for implicit feedback (Hu, Koren and Volinsky) with confidence from the signal model, parallel solves, fold-in at request time and `Refresh` for changed subjects and objects.
- `algo/bpr`: Bayesian personalized ranking with matrix factorization.
- All factor models answer user→item, item→item (cosine on item factors) and user→user (cosine on user factors).
- `evaluation`: `Errors` with RMSE and MAE for rating prediction, `Predictor` interface.
- `recoant.ErrUnsupported` for request directions a model cannot answer; `knn.ErrUnsupported` is the same error.
- `internal/linalg`: dense linear algebra on the standard library: Gram-Schmidt, Cholesky, Jacobi eigendecomposition, randomized SVD.
- Results on recoant-data (precision@10, time split): on latent/implicit-feedback ALS 0.057 and BPR 0.050 beat user-kNN 0.037 and popularity 0.014; on basic/taste-clusters BPR 0.078 and ALS 0.074 beat item-kNN 0.070. On latent/explicit-ratings SVD++ reaches an RMSE of 0.79, biased MF 0.81, Funk-SVD 0.83, the global mean 1.02.
- Measured on benchmark/large (200,000 users, 50,000 items, 8 CPUs): PureSVD with 64 factors fits in 7.7 s, ALS (32 factors, 10 iterations) in 8.1 s and refreshes after 2,000 new signals in 0.12 s, BPR (30 epochs) in 14.3 s. A request scores all objects: 1.8–3 ms; approximate nearest neighbor search is planned for v0.8.
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
