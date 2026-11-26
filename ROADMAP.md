# Roadmap

> **A configurable recommendation framework in Go for streams of user signals — batch, incremental and online, in every direction: user to entity, entity to entity, user to user, one-way or reciprocal.**

The design is described in [docs/architecture.md](docs/architecture.md).

Until v1.0.0 minor versions may change the API; every breaking change is listed in the [changelog](CHANGELOG.md) with migration notes. Patch versions never break the API.

## Overview

| Version | Theme | Status |
| --- | --- | --- |
| v0.1.0 | Core data model and baselines | released |
| v0.2.0 | Neighborhood models and user-to-user | released |
| v0.3.0 | Latent factors: SVD/PCA, MF, SVD++, ALS, BPR | released |
| v0.4.0 | Signals 2.0 and configuration | released |
| v0.5.0 | Attributes | released |
| v0.6.0 | User-to-user: roles and reciprocal matching | released |
| v0.7.0 | Segments and profiles | in progress |
| v0.8.0 | Streaming: online and incremental learning | planned |
| v0.9.0 | Funnels and learned signal relevance | planned |
| v0.10.0 | Feature models and cold start | planned |
| v0.11.0 | Graph, hybrids and re-ranking | planned |
| v0.12.0 | Serving and performance | planned |
| v0.13.0 | Server: HTTP API around the library | planned |
| v1.0.0 | Stable API | planned |

Every version from v0.4 on extends the [configuration](docs/architecture.md#configuration) with its own section, so that applications model kinds, attributes, relations, signals, models, matching and serving without code.

## v0.1.0 — Core data model and baselines

- Core types: `Ref`, `Kind`, `Request`, `Scored`, `Recommender` and the learning interfaces
- Signals with type, value and time; signal model with weights, value transforms and time decay
- Interaction store with ID dictionaries and sparse matrices
- Popularity and trending baselines
- Evaluation: precision, recall, NDCG, MAP and hit rate at k, coverage; temporal and leave-one-out splits
- Persistence of stores and models, JSON or binary
- Synthetic test sets with known structure live in the separate repository [recoant-data](https://github.com/breskos/recoant-data), not in this one

## v0.2.0 — Neighborhood models and user-to-user

- Item-kNN and user-kNN with cosine, Jaccard and adjusted cosine similarity
- Incremental co-occurrence counts
- User-to-user and entity-to-entity recommendations
- Exclusion of seen entities, filters

## v0.3.0 — Latent factors

- Truncated SVD and PCA (randomized), PureSVD, fold-in of new users
- Funk-SVD, biased MF and SVD++ trained with SGD (explicit feedback)
- ALS for implicit feedback with confidence from the signal model
- BPR

## v0.4.0 — Signals 2.0 and configuration

- Feedback kinds per signal type: implicit, rating (with scale and neutral point), negative
- Channels per relation with aggregation (sum, last, max, mean) and half-life; a re-rating replaces the old rating
- Named relations; several relations between the same kinds
- Negative feedback in all models: excluded from recommendations, signed strengths in kNN, confident zeros in ALS, hard negatives in BPR, low targets in MF
- `config` package: versioned JSON configuration for kinds, relations, signals, models and serving, validated with precise errors; builds store, signal model and models
- Migration notes for the changed store and signal model APIs

## v0.5.0 — Attributes

- Attribute schemas per kind: category, number, set, geo; sensitive attributes
- Attribute store fed by events (set, update, delete), dense columns
- Attribute filters as data in requests and configuration: equality, ranges, set overlap, distance; mutual filters for matching
- Entity deletion removes attributes, signals and vectors; retention per relation
- `privacy` section in the configuration: retention, deletion of attributes with the entity, export of sensitive attributes
- Test sets with attributes in recoant-data (people discovery, shop) and an end-to-end check

## v0.6.0 — User-to-user: roles and reciprocal matching

- Taste (subject role) and appearance (object role) vectors for every relation of a factor model
- Reciprocal recommender, configurable per relation: one-way or reciprocal; harmonic, geometric, minimum or product combination; minimum per direction
- Exposure balancing for popular profiles
- Collective factorization: shared subject vectors across relations (user → item ← user)
- Evaluation for reciprocal recommendations (mutual hit rate), test sets in recoant-data

## v0.7.0 — Segments and profiles

- k-means and mini-batch k-means on vectors and attributes
- Segment profiles: size, typical objects, attribute distributions, activity, negative share
- Entity profiles for assessing users: segment, position, activity, positive/negative ratio, nearest entities
- Segments as filters, features and evaluation slices

## v0.8.0 — Streaming

- Stream pipeline: ingest, sessionization, windows, decay
- Online updates for popularity, kNN, SGD-based MF and BPR; incremental updates for ALS
- Prequential evaluation and drift detection
- Session-based recommendations

## v0.9.0 — Funnels and signal relevance

- Funnel stages, depth per pair, transition and conversion rates
- Funnel-aware interaction strength
- Learned signal relevance against a configurable target stage

## v0.10.0 — Feature models and cold start

- Factorization machines / LightFM-style models with attribute embeddings
- Taste and appearance vectors for new entities from their attributes

## v0.11.0 — Graph, hybrids and re-ranking

- Typed, weighted relation graph; P3α, RP3β and personalized PageRank
- Weighted and cascaded hybrids
- Re-ranking for diversity, novelty and business rules
- Explanations ("because you liked …")

## v0.12.0 — Serving and performance

- Persistence of all models
- Compact custom binary format for large matrices
- Snapshots for concurrent reads while learning
- Approximate nearest neighbor search on embeddings
- Benchmarks at scale

## v0.13.0 — Server

- recoant as a server: HTTP API to ingest signals and attributes and query recommendations, driven by the configuration
- Health and metrics endpoints
