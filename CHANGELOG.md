# Changelog

All notable changes to this project are documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [Semantic Versioning](https://semver.org/). Before v1.0.0 minor versions may break the API; every break has migration notes.

## [Unreleased]

### Added
- **`segment` package**: `segment.KMeans` (k-means++ seeds, 10 restarts by default, or mini-batch k-means with `BatchSize`) and `segment.Segmenter`, which clusters the entities of a kind on taste or appearance vectors of a factor model and on attributes (categories and sets one-hot, numbers and locations standardized, every feature block scaled to the same size, then weighted). Sensitive attributes need `AllowSensitive`. `Segment` assigns new entities to the nearest center; `Profiles` describes each segment (size, share, activity, negative share, typical objects, attribute distributions); `Profile` describes an entity (segment, relative distance to the center, positive and negative interactions, nearest entities).
- **Roles**: factor models have `Taste(ref)` and `Appearance(ref)`.
- **Configuration**: the `segments` section per kind (`k`, `features`, `weights`, `model`, `relation`, `batch_size`, `iterations`, `restarts`, `seed`, `allow_sensitive`, `attribute`). Fit writes every entity's segment into a category attribute (default `segment`, values `s0`, `s1`, …), so filters can use it, e.g. `{"attribute": "segment", "same": true}`. `Engine.Segmenter` returns the segmenter of a kind.
- **Stable segment numbers**: after a refit every segment keeps the number of the nearest old segment.
- **Evaluation**: `evaluation.BySegment` reports the metrics per segment.
- **examples/segments** measures segments on basic/taste-clusters and segments/customers; results in [docs/measurements.md](docs/measurements.md).
- **Use case** `examples/usecases/customers`: customer segments from behavior and age, segment and customer profiles, "customers like me", new customers placed immediately.
- **Data**: the new data set `segments/customers` of recoant-data (6 hidden segments that need behavior and attributes together).

### Changed
- The `segments` section of the configuration is accepted (it was rejected as planned before).

## [0.6.0] - 2026-11-26

Reciprocal matching, roles and collective factorization.

### Added
- **`match` package**: `match.Reciprocal` recommends pairs where both sides must agree. The forward direction comes from any recommender, the reverse from a `match.Scorer`; both are calibrated to [0, 1] (`clamp` or `sigmoid`) and combined with a harmonic or geometric mean, the minimum or the product, with a minimum per direction. Candidates who rejected the subject are left out, and exposure balancing divides by `(1 + n/n̄)^β` so that popular profiles are recommended less often.
- **Roles**: every factor model (ALS, BPR, MF variants, PureSVD) has `Interest(subjects, object)`, the subjects' taste vectors times the object's appearance vector, which scores the reverse direction.
- **Collective factorization**: `als.Config.Shared` and `SharedWeight` let further relations with the same subject kind train the subject vectors, so a user's taste learns from likes and purchases alike.
- **Configuration**: the `matching` section per relation (`mode` one_way or reciprocal, `combine`, `calibrate`, `min_each`, `exposure_balancing`, `pool`, `reverse_model` for relations between different kinds), and `shared` for ALS models. Every model of a reciprocal relation is served reciprocally.
- **Evaluation**: `evaluation.Mutual` measures hit rate, mutual hit rate, mutual precision and the Gini concentration of recommendations.
- **examples/matching** measures reciprocal matching on the new data set `matching/dating` of recoant-data; results in [docs/measurements.md](docs/measurements.md).
- **Use cases** in `examples/usecases`: six runnable stories (online shop, dating, music, recipes, events, job board), each with a configuration and a README that explains how to model the application and which design decisions to copy; a gallery page maps "I want …" to features. CI runs them.

### Changed
- The `matching` section of the configuration is accepted (it was rejected as planned before).

## [0.5.0] - 2026-11-26

Attributes.

### Added
- **`attr` package**: attribute schemas per kind with the types `category`, `number`, `set` and `geo` and a `sensitive` flag; an attribute store with dense columns per attribute, fed by events (`attr.Event`: set, update, delete of an attribute or a whole entity); `Attributes` for profiles and exports leaves sensitive attributes out unless asked; error messages never contain attribute values.
- **Attribute filters as data**: `recoant.Condition` and `recoant.Request.Where` with the tests `in` (category or set overlap), `min`/`max`, `max_distance_km` (from the subject's location or `near`), `within` (between two attributes of the subject, e.g. `min_age` and `max_age`), `mutual` (both sides' preferences must be met) and `same`. `attr.Filtered` wraps any recommender and evaluates them; `attr.Schema.Check` validates them.
- **Deletion**: `store.Store.Delete` removes a reference's rows and columns in all relations, `popularity.Popularity.Delete` its score, `attr.Store.Delete` its attributes. Dense indexes stay as tombstones (`store.Store.Deleted`), the ID is forgotten; recommenders never return deleted references, also before the next Fit.
- **Retention** per relation: `signal.RelationSpec.Retention` and `store.Store.Expire` drop pairs whose last signal is older than the retention, in all channels.
- **Configuration**: attributes of kinds are used; `serving.*.filters` and `serving.*.subject`; the `privacy` section with `retention` per relation, `delete_attributes_with_entity` (default true) and `export_sensitive`. `Engine.Apply` records attribute events, `Engine.Delete` removes a reference everywhere, `Engine.Expire` applies retention, `Engine.ExportAttributes` exports attributes, `Engine.Recommend` and `Engine.Request` take additional conditions.
- `persist.SaveAttributes` and `persist.LoadAttributes`.
- `examples/config/people.json` now has mutual age preferences, a distance filter and privacy settings.
- [docs/measurements.md](docs/measurements.md): quality and performance on the recoant-data sets, with the raw output of every run in `docs/measurements/`, references and what the results mean in production.
- `examples/benchmark`: ingestion, memory, training, refresh, latency and throughput of every model on benchmark/large; `-scale` for the store at 40 million interactions.
- `examples/evaluate` reports a random floor and an oracle with the hidden structure of each data set.
- `store.Store.History` returns a subject's objects and whether it rejected them in one call.
- `examples/attributes`: end-to-end check on the new recoant-data sets attributes/people and attributes/shop. With filters, precision@10 rises from 0.005 to 0.012 for people discovery (mutual age preferences and 50 km) and from 0.009 to 0.012 in the shop (budget and shared tags); no deleted reference is recommended, retention drops exactly the expected pairs.

### Changed
- Store snapshots are version 3 (tombstones and the time of the last signal per pair for relations with a retention); versions 1 and 2 still load, with the latest signal's time for every pair.
- `recoant.Request.Validate` returns `recoant.ErrUnfiltered` for requests with conditions, so recommenders never ignore them silently.
- `store.Store.Len` counts the indexes of deleted references as well; `store.Store.Ref` returns false for them.

### Fixed
- Popularity asked the store for every candidate whether the subject had seen or rejected it; it now reads the subject's history once per request: 13× lower latency, 63× more requests per second on 8 cores (benchmark/large).
- Item-kNN and user-kNN kept the full sorted similarity list of every row behind its top neighbors: user-kNN on benchmark/large needs 101 MB instead of 2 GB and refreshes 4× faster.
- The v0.3 entry announced approximate nearest neighbor search for v0.8; it is planned for v0.12.

### Migration
- Code that calls `Request.Validate` in its own recommender gets `ErrUnfiltered` for requests with `Where`; wrap it with `attr.Filtered`.
- Code that iterates `store.Store.Len` indexes must skip those for which `Ref` returns false.

## [0.4.0] - 2026-11-26

Signals 2.0 and configuration.

### Added
- **Feedback kinds** in the signal model: `signal.Implicit` (default), `signal.Rating` with `Scale` and `Neutral` (a 1-star rating on a 1–5 scale with neutral 3 counts negative) and `signal.Negative` (dislike, pass, return, block).
- **Channels** per relation with their own aggregation — `signal.Sum`, `signal.Last` (a re-rating replaces the old rating), `signal.Max`, `signal.Mean` — and half-life. Defaults: `positive`, `negative` and `rating`. `store.ChannelMatrix` returns the raw values of a channel, `store.Channels` lists them.
- **Named relations**: `Rule.Relation`, `store.Relation.Name`, `signal.RelationSpec` with kinds, half-life and channel settings per relation. Signals for a declared relation must connect its kinds.
- **Preference**: `store.Matrix`, `Row`, `Strength` and `Interactions` return the signed preference, the sum over channels of weight·(value − neutral).
- **Negative feedback in the models**: rejected objects are never recommended unless `recoant.Request.IncludeRejected` is set (`store.Rejected`, `recoant.Rejecter`); popularity counts negative signals down; Jaccard ignores negative interactions; cosine kNN uses signed strengths; ALS treats negative preference as confident zeros; BPR samples rejected objects as hard negatives; `mf.Config.Channel` trains on a channel such as the ratings.
- **`config` package**: versioned JSON configuration with kinds (and attribute schemas, used from v0.5), relations, channels, signals, models and serving; strict validation with field paths in errors; `Config.Build` creates an `Engine` with store and models, `Engine.Recommend` answers a serving declaration with fallback. Durations as strings, including days (`"30d"`).
- Example configurations: `examples/config/shop.json`, `examples/config/people.json`.

### Changed
- `signal.Model.Weight` returns the signed contribution (negative for negative feedback, centered for ratings). `Strength` uses the decay of the signal's channel.
- `signal.Model.ChannelNames` and `ChannelSpec` take the relation name.
- Store snapshots are version 2 (channels per relation); version 1 snapshots still load as one `positive` channel.

### Migration
- `store.Relation` has a `Name` field. Code that builds `store.Relation{Subject: …, Object: …}` keeps working for signals whose rule names no relation.
- Code that relied on `Weight` being non-negative must handle negative values.
- Recommenders now exclude rejected objects by default; set `IncludeRejected` to get the previous behavior.

## [0.3.0] - 2026-11-26

Latent factor models.

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
- Measured on benchmark/large (200,000 users, 50,000 items, 8 CPUs): PureSVD with 64 factors fits in 7.7 s, ALS (32 factors, 10 iterations) in 8.1 s and refreshes after 2,000 new signals in 0.12 s, BPR (30 epochs) in 14.3 s. A request scores all objects: 1.8–3 ms; approximate nearest neighbor search is planned for v0.12.

## [0.2.0] - 2026-11-26

Neighborhood models and user-to-user recommendations.

### Added
- `algo/knn`: item-kNN and user-kNN with cosine, Jaccard and adjusted cosine similarity, shrinkage, a limit for very active rows, and parallel training. Directions: user→item, item→item (similar items), user→item via similar users and user→user (similar users). `Refresh` updates only the rows that changed in the store since the last training. Measured on recoant-data benchmark/large (200,000 users, 50,000 items, 1.5 million interactions, 8 CPUs): item-kNN fits in 0.15 s and refreshes after 2,000 new signals in 0.06 s; user-kNN fits in 9.7 s and refreshes in 7.7 s, or 6 s and 0.8 s with `MaxDegree: 1000`.
- `store`: `Seq` and `Changes` tell which subjects and objects changed since a point in time, `Row` returns a subject's interactions by dense index.
- `examples/evaluate`: compares popularity, item-kNN and user-kNN on the recoant-data sets.

## [0.1.0] - 2026-11-26

Core data model and baselines.

### Added
- Core types in `recoant`: `Ref`, `Kind`, `Signal`, `SignalType`, `Request`, `Scored` and the `Recommender`, `Observer` and `Seen` interfaces.
- `signal`: signal model with weights per signal type, value transforms (constant, linear, log, saturate, threshold, cap) and a half-life.
- `store`: incremental interaction store with interned IDs, sorted sparse rows and forward decay; snapshots of relations as CSR matrices. Measured: 40 million interactions of 2 million users with 500,000 items in 0.73 GB heap.
- `sparse`: CSR matrices with transpose, row and column sums and matrix-vector product.
- `algo/popularity`: all-time popularity and trending (half-life), online and batch.
- `evaluation`: precision, recall, NDCG, MAP, hit rate and MRR at k, coverage; splits by time, by fraction and leave-last-out; parallel evaluation.
- `persist`: save and load stores, signal models and popularity models in JSON or binary, detected on load, written atomically.
- Project setup: module, CI (vet, race tests, coverage gate, golangci-lint), contribution guidelines and templates.

[Unreleased]: https://github.com/breskos/recoant/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/breskos/recoant/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/breskos/recoant/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/breskos/recoant/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/breskos/recoant/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/breskos/recoant/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/breskos/recoant/releases/tag/v0.1.0
