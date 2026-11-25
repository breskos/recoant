# Architecture (draft)

*State: draft. v0.1 (core types, signal model, store, popularity, evaluation, persistence) the neighborhood models of v0.2 and the latent factor models of v0.3 are implemented; the rest is planned.*

recoant is a framework for recommendation engines in Go. It takes a stream of
signals (visits, clicks, dwell time, purchases, …) between anonymized users and
entities, turns them into weighted interactions and relations, and serves
recommendations from several interchangeable algorithms — trained in batch,
updated incrementally or learned online from the stream.

## Goals

- **Stream first.** Signals arrive as events. Every component can consume them
  one at a time; batch training is a stream that is replayed.
- **Batch, incremental and online.** Models can be trained from scratch,
  updated with new data without full retraining, or learn per event.
- **Signals have depth.** A visit is not a purchase. Signal types carry
  weights, values (e.g. dwell time) are transformed, old signals decay, and the
  relevance of each signal type can be learned.
- **Funnels.** Signals belong to stages (visit → click → dwell → cart →
  purchase). The deepest stage reached and the transition probabilities
  between stages are first-class information.
- **Relations.** Users and entities are nodes in a typed graph: user→entity
  (interactions), entity→entity (same category, bought together),
  user→user (follows, similar). Recommendations can go in every direction.
- **Many algorithms, one interface.** Popularity, neighborhood models, latent
  factor models (SVD/PCA, matrix factorization as in the Netflix Prize, ALS,
  BPR), graph walks and hybrids all answer the same request.
- **Measurable.** Offline ranking metrics with temporal splits, and
  prequential (test-then-train) evaluation for streams.
- **Embeddable.** A library, not a server. Safe for concurrent reads while it
  learns.

## Non-goals (for now)

- A standalone server, message-queue connectors or a UI. These can be built on
  top as separate modules.
- Deep-learning recommenders (two-tower, transformers).
- Storing personal data. IDs arrive anonymized; recoant only sees opaque IDs.

## Core concepts

### Entities and IDs

```go
type Kind string // "user", "article", "product", "category", …

type Ref struct {
	Kind Kind
	ID   string // opaque, already anonymized
}
```

Internally every `Ref` is interned into a dense `uint32` index per kind, so
models work on compact arrays and sparse matrices. The dictionary is
persisted with the model.

### Signals

```go
type SignalType string // "visit", "click", "dwell", "cart", "purchase", …

type Signal struct {
	Subject Ref       // usually a user
	Object  Ref       // usually an entity, may be a user
	Type    SignalType
	Value   float64   // e.g. dwell seconds, quantity; 1 if not applicable
	Time    time.Time
	Session string    // optional
}
```

### Signal model: from signals to interaction strength

A `signal.Model` turns a signal into a contribution to the strength of the
(subject, object) pair:

```
strength += weight(type) · transform(type, value) · decay(now − time)
```

- **Weights** per signal type: configured, or **learned** from how well a
  signal type predicts a target stage (signal relevance).
- **Transforms** for values: log, saturating (dwell time beyond 3 min adds
  little), bucketed, thresholds (dwell < 5 s counts as bounce).
- **Decay**: exponential with a half-life, or none.
- **Confidence**: implicit-feedback models (ALS) use strength as confidence,
  `c = 1 + α·strength`.

### Funnels

A `funnel.Funnel` is an ordered list of stages, each mapped to one or more
signal types. Per pair, user, entity or globally it tracks:

- the deepest stage reached (a purchase implies a click),
- stage counts and transition probabilities (click-through, add-to-cart rate,
  conversion),
- drop-off.

Uses: funnel-aware weights (depth beats count), entity quality features
(conversion rate, not just popularity), targets for learned signal relevance,
and recommendation objectives ("optimize for purchase", "optimize for
engagement").

### Interaction store and relation graph

- `store`: sparse user×entity interaction matrix (CSR/CSC plus a mutable
  delta for incremental updates), per signal type and aggregated.
- `graph`: typed, weighted multi-relational graph of `Ref`s. Interactions are
  one relation type; others are added explicitly (entity belongs to category,
  user follows user).

### Requests and results

```go
type Request struct {
	Subject Ref        // who or what to recommend for
	Target  Kind       // what to recommend: "product", "user", …
	K       int
	Exclude []Ref      // e.g. already seen
	Filter  func(Ref) bool
	Context map[string]string
}

type Scored struct {
	Ref   Ref
	Score float64
}

type Recommender interface {
	Recommend(ctx context.Context, req Request) ([]Scored, error)
}
```

The direction follows from `Subject.Kind` and `Target`:

| Subject | Target | Example |
| --- | --- | --- |
| user | entity | "for you" |
| entity | entity | "similar items", "bought together" |
| user | user | "people like you", "who to follow" |
| entity | user | audience targeting |

### Learning modes

```go
type Trainer interface {        // batch
	Fit(ctx context.Context, s *store.Store) error
}

type Incremental interface {    // update with a batch of new signals
	Update(ctx context.Context, signals []signal.Signal) error
}

type Online interface {         // learn per event
	Observe(sig signal.Signal) error
}
```

Each algorithm implements what it supports. The `stream` package drives them:
ingest, sessionize, window, decay, update models, evaluate prequentially and
detect drift.

## Algorithms

| Family | Algorithms | Batch | Incremental | Online |
| --- | --- | --- | --- | --- |
| Baselines | popularity, trending (time-decayed), random | ✓ | ✓ | ✓ |
| Neighborhood | item-kNN, user-kNN (cosine, Jaccard, adjusted cosine), co-occurrence | ✓ | ✓ | ✓ |
| Latent factors (linear algebra) | truncated SVD / PCA (randomized), PureSVD | ✓ | fold-in | — |
| Matrix factorization | Funk-SVD, biased MF, SVD++ (Netflix Prize) | ✓ | ✓ | ✓ (SGD) |
| Implicit feedback | ALS (Hu, Koren, Volinsky), BPR | ✓ | ✓ | BPR |
| Graph | P3α, RP3β random walks, personalized PageRank | ✓ | ✓ | — |
| Session | session-kNN, sequential co-occurrence | ✓ | ✓ | ✓ |
| Hybrid | weighted blend, cascade, re-ranking (diversity, novelty, business rules) | ✓ | ✓ | ✓ |

User-to-user comes from user-kNN, user embeddings of the latent models and the
graph.

## Package layout (proposal)

| Package | Purpose |
| --- | --- |
| `recoant` | Core types: `Ref`, `Kind`, `Signal`, `Request`, `Scored`, interfaces |
| `signal` | Signal model (weights, transforms, decay), later learned relevance |
| `funnel` | Funnel stages, depth, transition rates |
| `store` | Interaction store with ID dictionaries and incremental sparse rows |
| `sparse` | Sparse matrices (CSR) for algorithms, including your own |
| `graph` | Typed relation graph |
| `stream` | Ingest, sessions, windows, online/incremental pipeline |
| `algo/popularity`, `algo/knn`, `algo/svd`, `algo/mf`, `algo/als`, `algo/bpr`, `algo/graphwalk`, `algo/session`, `algo/hybrid` | Algorithms |
| `rerank` | Filters, diversity, business rules |
| `evaluation` | Precision/recall/NDCG/MAP@k, hit rate, coverage, novelty; temporal splits; prequential |
| `persist` | Versioned save and load in JSON or binary |
| `internal/linalg` | Dense linear algebra, built on the standard library only |

## Decisions

- **Standard library only.** No external modules, no cgo — not gonum and not
  gopher-learn. Sparse and dense linear algebra (`sparse`, `internal/linalg`:
  CSR/CSC, Cholesky and conjugate gradient for ALS, randomized SVD with QR and
  power iterations), drift detection and evaluation are built in recoant
  itself.
- **Scale: several million users and entities in one process.** IDs are
  interned to `uint32`, interactions are stored as sorted rows of
  `(uint32, float32)` pairs (8 bytes each), and every hot path works on dense
  indexes, not strings.
- **Forward decay.** Interaction strengths are stored relative to an epoch
  (`w · e^{λ(t − t₀)}`), so a signal is added in O(log row) without touching
  older values. Reading at time `now` multiplies by `e^{−λ(now − t₀)}`. The
  epoch is moved forward when the factors grow too large.
- **Persistence format is selectable:** JSON (readable, with a versioned
  envelope) or binary. Loading detects the format.
- **Signal relevance** is learned against a target stage that is configured
  per funnel (v0.5).
- **Test data lives outside the repository.** Synthetic data sets with known
  structure, and their generator, are in
  [recoant-data](https://github.com/breskos/recoant-data). Unit tests in
  recoant use small hand-made fixtures.
- **Server later.** recoant stays a library first; an HTTP/gRPC server with
  its own interface comes after the core (see roadmap). The concurrency-safe
  `Recommender` and `Observer` interfaces are what it will wrap.
