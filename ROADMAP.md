# Roadmap

> **A recommendation framework in Go for streams of user signals — batch, incremental and online, in every direction: user to entity, entity to entity, user to user.**

The design is described in [docs/architecture.md](docs/architecture.md).

Until v1.0.0 minor versions may change the API; every breaking change is listed in the [changelog](CHANGELOG.md) with migration notes. Patch versions never break the API.

## Overview

| Version | Theme | Status |
| --- | --- | --- |
| v0.1.0 | Core data model and baselines | done (merged) |
| v0.2.0 | Neighborhood models and user-to-user | in progress |
| v0.3.0 | Latent factors: SVD/PCA, MF, SVD++, ALS, BPR | planned |
| v0.4.0 | Streaming: online and incremental learning | planned |
| v0.5.0 | Signal relevance and funnels | planned |
| v0.6.0 | Relations and graph algorithms | planned |
| v0.7.0 | Hybrids and re-ranking | planned |
| v0.8.0 | Serving and performance | planned |
| v0.9.0 | Server: HTTP API around the library | planned |
| v1.0.0 | Stable API | planned |

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

## v0.4.0 — Streaming

- Stream pipeline: ingest, sessionization, windows, decay
- Online updates for popularity, kNN, SGD-based MF and BPR; incremental updates for ALS
- Prequential evaluation and drift detection
- Session-based recommendations

## v0.5.0 — Signal relevance and funnels

- Funnel stages, depth per pair, transition and conversion rates
- Funnel-aware interaction strength
- Learned signal relevance against a configurable target stage

## v0.6.0 — Relations and graph

- Typed, weighted relation graph between users and entities
- P3α, RP3β and personalized PageRank
- Relations as side information for the other models

## v0.7.0 — Hybrids and re-ranking

- Weighted and cascaded hybrids
- Re-ranking for diversity, novelty and business rules
- Explanations ("because you viewed …")

## v0.8.0 — Serving and performance

- Compact custom binary format for large matrices
- Snapshots for concurrent reads while learning
- Approximate nearest neighbor search on embeddings
- Benchmarks at scale

## v0.9.0 — Server

- recoant as a server: HTTP API to ingest signals and query recommendations
- Configuration, health and metrics endpoints
