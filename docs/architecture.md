# Architecture

*State: draft. Implemented: core types, signal model, store, popularity, evaluation and persistence (v0.1), neighborhood models (v0.2), latent factor models (v0.3), signals 2.0 and the configuration (v0.4), attributes, filters, deletion and retention (v0.5), roles, reciprocal matching and collective factorization (v0.6), segments and profiles (v0.7). Streaming is the design for the next version; see the [roadmap](../ROADMAP.md).*

recoant is a framework for recommendation engines in Go. It takes a stream of
signals between anonymized users and entities — visits, clicks, dwell time,
purchases, star ratings, likes and dislikes — turns them into weighted
relations, and serves recommendations from several interchangeable
algorithms: trained in batch, updated incrementally or learned online.

recoant is meant for very different applications: a shop, a news site, a
streaming service, a dating or people-discovery app. What differs between
them — which entities exist, how signals count, which relations matter,
which models run, whether matches must be mutual — is **modeling**, and
modeling is **configuration**, not code (see [Configuration](#configuration)).

## Goals

- **Configurable.** Entity kinds, attributes, relations, signal semantics,
  models and serving rules are declared in one configuration. The same
  binary serves a shop and a dating app.
- **Stream first.** Signals arrive as events. Every component can consume
  them one at a time; batch training is a stream that is replayed.
- **Batch, incremental and online.** Models are trained from scratch,
  updated with new data without full retraining, or learn per event.
- **Signals have depth and direction.** A visit is not a purchase, a 2-star
  rating is not a 5-star rating, and a dislike is evidence against an item.
  Implicit, explicit and negative feedback are first-class.
- **Relations in every direction.** Users and entities are nodes in a typed
  graph with named relations: user→item, item→item, user→user, and paths
  like user → item ← user. Recommendations go in every direction, including
  reciprocal user-to-user matching.
- **Entities have attributes.** Categorical, numerical and multi-valued
  attributes are used for filters, cold start, profiles and clustering.
- **Users can be understood.** Users are clustered into segments with
  profiles, and every user has an assessable profile.
- **Many algorithms, one interface.** Popularity, neighborhood models, latent
  factor models, graph walks and hybrids answer the same request.
- **Measurable.** Ranking metrics, rating errors, temporal splits and
  prequential evaluation for streams.
- **Embeddable and scalable.** A library first (a server comes later), pure
  Go, millions of users and entities in one process.

## Non-goals (for now)

- Message-queue connectors or a UI. These can be built on top.
- Deep-learning recommenders (two-tower networks, transformers).
- Identifying people. IDs arrive anonymized; see [Privacy](#privacy) for
  attributes.

## Core concepts

### Entities and IDs

```go
type Kind string // "user", "product", "article", "brand", …

type Ref struct {
	Kind Kind
	ID   string // opaque, already anonymized
}
```

Every `Ref` is interned into a dense `uint32` index per kind, so models work
on compact arrays and sparse matrices.

### Signals

```go
type Signal struct {
	Subject Ref        // who acts, usually a user
	Object  Ref        // what is acted on: an entity or another user
	Type    SignalType // "visit", "rating", "like", "pass", …
	Value   float64    // dwell seconds, stars, quantity; 1 otherwise
	Time    time.Time
	Session string     // optional
}
```

The signal itself carries no meaning beyond its type. What a type *means* is
configured.

## Signals 2.0: feedback, channels and relations

*Implemented in v0.4.*

### Feedback kinds

Every signal type has a feedback kind:

| Kind | Examples | Meaning |
| --- | --- | --- |
| `implicit` | visit, click, dwell, cart, purchase, share | positive evidence of interest; more is stronger |
| `rating` | stars 1–5, thumbs up/down, 1–10 score | an explicit judgment on a scale with a neutral point |
| `negative` | dislike, pass, skip, hide, return, report | evidence against interest |

A **rating** type declares its scale and neutral point, e.g. stars from 1 to
5 with neutral 3. The centered value `value − neutral` is positive above the
neutral point and negative below: a 1-star rating is negative feedback.
Models for explicit feedback use the raw value, all others the centered one.

### Channels and aggregation

The store keeps, per relation and pair, several **channels** instead of one
sum. Each signal type writes into one channel; each channel has an
aggregation and a decay:

| Aggregation | Use |
| --- | --- |
| `sum` | implicit and negative feedback: more signals, more evidence |
| `last` | ratings: a new rating replaces the old one |
| `max` | the deepest funnel stage reached |
| `mean` | averaged scores |

The default channels are `positive` (sum, decaying), `negative` (sum,
decaying) and `rating` (last, no decay). A configuration can add more, for
example a separate `purchase` channel for a model that only learns from
purchases. Forward decay still applies per channel.

### Named relations

A relation is a named, directed connection between two kinds:

```
user  —buys→      product      (channels: positive, negative, rating)
user  —likes→     user         (like/superlike positive, pass negative)
user  —follows→   user
product —by_brand→ brand        (static, from attributes or a feed)
```

Signal types are mapped to relations in the configuration, so `like` and
`pass` both land on `likes`, positive and negative. Several relations can
connect the same kinds (`likes` and `messages` between users). Paths like
user → item ← user (users who like the same items) are derived from these
relations by the models.

### Negative feedback in the models

| Model | Use of negative feedback |
| --- | --- |
| all | never recommend what the subject rejected (configurable) |
| popularity | negative signals lower the score |
| kNN | signed strengths; similarity can be negative ("people who disliked this…") |
| ALS | preference 0 with high confidence `1 + β·negative` (instead of a weak missing value) |
| BPR | rejected objects are sampled as hard negatives |
| MF, SVD++ | ratings below the neutral point are regular low targets |

## Roles: taste and appearance

*Implemented in v0.6 (package `match`, `als.Config.Shared`).*

Every entity can appear as subject and as object of a relation. A latent
factor model of a relation learns one vector per role:

- **taste** (subject role): what the entity is drawn to,
- **appearance** (object role): how it is perceived by others.

The interest of `u` in `v` is `taste(u) · appearance(v)`. For user→product
this is the usual user and item factor; for `user —likes→ user` it is
exactly the dating case: whom I like vs. who likes people like me.

**Shared vectors across relations.** With collective factorization, a user
has one taste vector that learns from all its relations as subject (e.g.
`buys` and `likes`), so behavior in one relation informs the other, and
users who like the same items become comparable (user → item ← user). In recoant, ALS takes
`shared` relations: their signals train the subject vectors too, and
recommendations are made for the model's own relation.

Every factor model (ALS, BPR, the MF variants, PureSVD) exposes both roles
through `Interest(subjects, object)`: the subjects' taste vectors times the
object's appearance vector. That is the reverse direction of matching.

**Reciprocal matching.** For relations where both sides must agree, a
reciprocal recommender combines both directions:

```
match(u, v) = combine( P(u likes v), P(v likes u) )
```

`combine` is configurable per relation: harmonic mean (default for
matching), geometric mean, minimum or product. Further settings:

- `mode`: `one_way` (follow, discovery) or `reciprocal` (dating, mentoring,
  job matching),
- a minimum for each direction,
- **exposure balancing**: very popular profiles are recommended less often,
  so that attention is spread and more mutual matches happen,
- exclusion of pairs where either side already passed,
- a calibration of the scores to [0, 1] before combining: `clamp` for ALS,
  `sigmoid` for BPR and MF,
- for relations between different kinds (candidate → job), a
  `reverse_model` of the reverse relation (job → candidate) scores the
  other direction.

The forward model proposes a pool of candidates (default 20·k, at least
200), with all filters and exclusions of the request; the reverse direction
is scored for the pool only. Exposure balancing divides by
`(1 + n/n̄)^β`, where `n` counts the subjects with a positive preference for
the candidate. Measured effect: [measurements](measurements.md#matchingdating--reciprocal-matching).

## Attributes

*Implemented in v0.5 (package `attr`); cold start, profiles and clustering
use attributes from v0.7 and v0.10.*

Every kind has a declared attribute schema:

| Type | Examples | Use |
| --- | --- | --- |
| `category` | city, brand, language | filters, features, cluster profiles |
| `number` | age, price, rating count | range filters, features (scaled) |
| `set` | tags, interests | overlap filters, features |
| `geo` | location (lat, lon) | distance filters |

Attributes are written as events like signals (set, update, delete per
entity) and kept in an attribute store with dense columns per attribute.

### Filters

Filters are data (`recoant.Condition`), so they come from the
configuration, from a request in Go or, later, from an HTTP request.
`attr.Filtered` wraps any recommender and turns the conditions of
`Request.Where` into its `Filter`; a recommender that receives conditions
nobody evaluated returns `ErrUnfiltered` instead of ignoring them.

| Test | Example | Meaning |
| --- | --- | --- |
| `in` | `{"attribute": "brand", "in": ["acme"]}` | category is one of, or set overlaps |
| `min`/`max` | `{"attribute": "price", "max": 50}` | number in range (inclusive) |
| `max_distance_km` | `{"attribute": "location", "max_distance_km": 50}` | distance to the subject's location, or to `near: [lat, lon]` |
| `within` | `{"attribute": "age", "within": ["min_age", "max_age"]}` | number between two attributes of the subject |
| `within` + `mutual` | `{…, "mutual": true}` | and the subject's value between the candidate's two attributes: both sides' preferences are met |
| `same` | `{"attribute": "interests", "same": true}` | category equal to, or set overlapping, the subject's |

A candidate without the attribute fails a condition. A condition that
refers to attributes the subject does not have is skipped, and the mutual
direction is skipped for a candidate without preferences: missing
preferences mean "no restriction", missing facts mean "no match".

Uses:

- **Filters in requests**, declared in the configuration or per request:
  `age between 25 and 35`, `distance < 50 km`, `category in [shoes, bags]`,
  including mutual filters for reciprocal matching (both sides' preferences
  must be met).
- **Cold start:** feature-based factorization (like LightFM or factorization
  machines) learns embeddings for attribute values, so a new profile or
  product gets taste and appearance vectors from its attributes alone.
- **Profiles and explanations:** "recommended because you like hiking".
- **Clustering** input.

## Segments and profiles

*Implemented in v0.7 (package `segment`); funnel stages in profiles follow
with v0.9, segments as model features with v0.10.*

- **Clustering** of subjects (and objects) on their taste/appearance vectors
  and attributes: k-means (k-means++ seeds, 10 restarts by default) for
  batch, mini-batch k-means for large sets, with a configurable number of
  segments and feature weights. Categories and sets are one-hot, numbers
  and locations standardized; every feature block is scaled to the same
  average size before its weight applies, so no block dominates by its
  number of dimensions. Sensitive attributes need `allow_sensitive`.
- **Segment profiles**: size, share, typical objects, attribute
  distributions, activity, share of negative feedback.
- **Entity profiles**: segment, distance to the segment center (relative to
  the segment's mean), activity, positive and negative interactions, most
  similar entities. Profiles are the basis for assessing users and for
  explanations. New entities are assigned to the nearest center.
- **Segments as filters**: every entity's segment is stored as a category
  attribute (default `segment`, values `s0`, `s1`, …), so the usual filters
  apply, e.g. `{"attribute": "segment", "same": true}`.
- **Per-segment evaluation**: `evaluation.BySegment`.
- **Stable numbers**: after a refit every segment keeps the number of the
  nearest old segment, so stored segment attributes keep their meaning.
- **Choosing features**: combine behavior (taste) with the attributes that
  separate groups of the same taste; leave out features without
  information, which weigh as much as the others and blur the segments
  (measured below).

Measured effect: [measurements](measurements.md#basictaste-clusters--segments).

## Funnels and signal relevance

*Planned for v0.9.* A funnel is an ordered list of stages mapped to signal
types (visit → click → dwell → cart → purchase). recoant tracks the deepest
stage per pair and transition rates per entity, and learns the relevance of
each signal type against a configured target stage (e.g. purchase) to
replace hand-set weights.

## Requests

```go
type Request struct {
	Subject     Ref            // who or what to recommend for
	Target      Kind           // what to recommend
	K           int
	Exclude     []Ref
	ExcludeSeen bool
	Filter      func(Ref) bool
	Where       []Condition // attribute filters as data, since v0.5
	Time        time.Time
}
```

Planned additions: the relation to use (by name) and the reciprocity mode.

| Subject | Target | Example |
| --- | --- | --- |
| user | entity | "for you" |
| entity | entity | "similar items" |
| user | user | "people like you", "who to follow", "matches" |
| entity | user | audience targeting |

## Configuration

*Implemented in v0.4 for kinds, relations, signals, models and serving, in
v0.5 for attributes, serving filters and privacy, in v0.6 for matching and
shared relations; every later version extends the schema. Sections of
later versions are rejected with the version that introduces them;
v0.7 adds segments.*

The modeling of an application lives in one configuration file. JSON,
because recoant uses the standard library only; the schema is versioned and
validated on load with error messages that point to the offending field.

| Section | Declares |
| --- | --- |
| `kinds` | entity kinds and their attribute schemas, which attributes are sensitive |
| `relations` | named relations between kinds, their channels, aggregation and half-life |
| `signals` | per signal type: relation, feedback kind, channel, weight, transform, scale |
| `models` | which algorithms run on which relations with which parameters |
| `matching` | one-way or reciprocal per relation, combination, calibration, minimum per direction, exposure balancing, candidate pool, reverse model |
| `serving` | defaults per request type: exclude seen, exclude rejected, filters, fallback model |
| `segments` | clustering: which kind, features, number of segments |
| `privacy` | retention per relation, deletion of attributes with the entity, export of sensitive attributes |

### Example: shop

[examples/config/shop.json](../examples/config/shop.json) (loads and builds
in v0.4):

```json
{
  "recoant": 1,
  "half_life": "30d",
  "kinds": {
    "user": {},
    "product": {
      "attributes": {
        "category": {"type": "category"},
        "brand": {"type": "category"},
        "price": {"type": "number"}
      }
    }
  },
  "relations": {
    "interacts": {"subject": "user", "object": "product"}
  },
  "signals": {
    "visit": {"relation": "interacts", "weight": 1},
    "click": {"relation": "interacts", "weight": 2},
    "dwell": {"relation": "interacts", "weight": 3, "transform": {"kind": "saturate", "param": 60}},
    "cart": {"relation": "interacts", "weight": 5},
    "purchase": {"relation": "interacts", "weight": 10},
    "stars": {"relation": "interacts", "feedback": "rating", "scale": [1, 5], "neutral": 3, "weight": 2},
    "return": {"relation": "interacts", "feedback": "negative", "weight": 8},
    "hide": {"relation": "interacts", "feedback": "negative", "weight": 20}
  },
  "models": {
    "main": {"algorithm": "als", "relation": "interacts", "params": {"factors": 64, "seed": 1}},
    "similar": {"algorithm": "item-knn", "relation": "interacts", "params": {"shrink": 10}},
    "ratings": {"algorithm": "svd++", "relation": "interacts", "channel": "rating", "params": {"factors": 16, "seed": 1}},
    "trending": {"algorithm": "popularity", "params": {"half_life": "2d"}}
  },
  "serving": {
    "for_you": {"model": "main", "exclude_seen": true, "fallback": "trending", "k": 20},
    "similar": {"model": "similar", "k": 10},
    "top": {"model": "trending", "target": "product"}
  }
}
```

### Example: dating

The target configuration; it works as shown from v0.7. The part that
works in v0.6 — attributes, reciprocal matching, mutual age preferences,
distance filter and privacy — is in
[examples/config/people.json](../examples/config/people.json).

```json
{
  "recoant": 1,
  "kinds": {
    "user": {
      "attributes": {
        "age":       {"type": "number", "sensitive": true},
        "min_age":   {"type": "number"},
        "max_age":   {"type": "number"},
        "gender":    {"type": "category", "sensitive": true},
        "location":  {"type": "geo", "sensitive": true},
        "interests": {"type": "set"}
      }
    }
  },
  "relations": {
    "likes": {"subject": "user", "object": "user", "half_life": "1440h"}
  },
  "signals": {
    "view":      {"relation": "likes", "feedback": "implicit", "weight": 0.5},
    "like":      {"relation": "likes", "feedback": "implicit", "weight": 5},
    "superlike": {"relation": "likes", "feedback": "implicit", "weight": 10},
    "message":   {"relation": "likes", "feedback": "implicit", "weight": 8},
    "pass":      {"relation": "likes", "feedback": "negative", "weight": 3},
    "block":     {"relation": "likes", "feedback": "negative", "weight": 50}
  },
  "models": {
    "attraction": {"algorithm": "als", "relation": "likes", "params": {"factors": 32}}
  },
  "matching": {
    "likes": {"mode": "reciprocal", "combine": "harmonic", "min_each": 0.1, "exposure_balancing": 0.5}
  },
  "serving": {
    "matches": {
      "model": "attraction", "target": "user", "exclude_seen": true,
      "filters": [
        {"attribute": "age", "within": ["min_age", "max_age"], "mutual": true},
        {"attribute": "location", "max_distance_km": 50}
      ]
    }
  },
  "segments": {
    "user": {"k": 20, "features": ["taste", "appearance", "interests"], "model": "attraction"}
  },
  "privacy": {"retention": {"likes": "365d"}, "delete_attributes_with_entity": true}
}
```

The Go API stays available for everything the configuration does; the
configuration builds the same objects.

## Algorithms

| Family | Algorithms | Status |
| --- | --- | --- |
| Baselines | popularity, trending | v0.1 |
| Neighborhood | item-kNN, user-kNN (cosine, Jaccard, adjusted cosine) | v0.2 |
| Latent factors | PureSVD/PCA (randomized SVD), Funk-SVD, biased MF, SVD++, ALS, BPR | v0.3 |
| Reciprocal | reciprocal combination of two directions, exposure balancing | ✓ |
| Collective | shared subject vectors across relations (ALS) | ✓ |
| Segments | k-means, mini-batch k-means | ✓ |
| Feature-based | factorization machines / LightFM-style with attributes (cold start) | v0.10 |
| Graph | P3α, RP3β, personalized PageRank | v0.11 |
| Session | session-kNN, sequential co-occurrence | v0.8 |
| Hybrid | weighted blend, cascade, re-ranking (diversity, novelty, business rules) | v0.11 |

## Package layout

| Package | Purpose | Status |
| --- | --- | --- |
| `recoant` | Core types and interfaces | ✓ |
| `signal` | Signal model: feedback kinds, weights, transforms, scales, channels, decay | ✓ |
| `store` | Channels per relation, ID dictionaries, change tracking | ✓ |
| `sparse` | Sparse matrices | ✓ |
| `config` | Loading and validating the configuration, building the objects | ✓ |
| `attr` | Attribute schemas, store and filters | ✓ |
| `match` | Reciprocal recommendation and exposure balancing | ✓ |
| `segment` | Clustering, segment and entity profiles | ✓ |
| `stream` | Ingest, sessions, windows, online/incremental pipeline | v0.8 |
| `funnel` | Funnel stages, rates, learned signal relevance | v0.9 |
| `algo/…` | popularity, knn, svd, mf, als, bpr ✓; fm, graphwalk, session, hybrid planned | |
| `rerank` | Filters, diversity, business rules | v0.11 |
| `evaluation` | Ranking metrics, rating errors, splits, prequential | ✓ |
| `persist` | JSON or binary | ✓ |
| `internal/linalg`, `internal/embed`, `internal/factor` | Linear algebra and shared factor-model code | ✓ |

## Privacy

IDs arrive anonymized, but attributes such as age, gender or location are
personal data even then. Therefore:

- Attributes are declared in the schema; `sensitive` attributes are never
  part of logs, explanations or exported profiles unless configured
  (`privacy.export_sensitive`). Error messages never contain attribute
  values. Snapshots hold them, since they are storage, not export.
- Every attribute and every entity can be deleted; deleting an entity
  removes its attributes, signals and vectors. Its dense index stays as a
  tombstone so that model indexes remain valid, but its ID is forgotten;
  the same ID later gets a new index. Signals and attributes are gone at
  once; vectors of trained models are never recommended again and are
  dropped at the next Fit or Refresh.
- Retention can be configured per relation (`privacy.retention`). The store
  aggregates signals per pair, so retention works per pair: a pair whose
  last signal is older than the limit is dropped in all channels by
  `Expire`. This costs one timestamp per pair, only for relations with a
  retention.
- Sensitive attributes are excluded from segments unless an application
  sets `allow_sensitive`, so that clustering does not group users by gender
  or age by accident; profiles leave them out too.

## Decisions

- **Standard library only.** No external modules, no cgo. Linear algebra,
  evaluation and drift detection are built in recoant.
- **Scale: several million users and entities in one process.** IDs are
  interned to `uint32`, interactions are stored as sorted rows of
  `(uint32, float32)` pairs, and hot paths work on dense indexes.
- **Forward decay** per channel: strengths are stored relative to an epoch,
  so adding a signal never rewrites older values.
- **Modeling is configuration.** Applications declare kinds, attributes,
  relations, signal semantics, models, matching and serving in a versioned
  JSON configuration; the Go API offers the same.
- **Feedback has a sign.** Ratings have a neutral point, negative signals are
  kept apart from positive ones, and rejected objects are not recommended by
  default.
- **Filters are data, evaluated in one place.** Conditions live in the
  request and the configuration; `attr.Filtered` evaluates them for every
  recommender, which never ignore them silently. Missing preferences mean
  no restriction, missing facts mean no match.
- **Deletion keeps indexes stable.** Deleted references become tombstones;
  the ID is forgotten, the index is not reused.
- **Matching is configurable** per relation: one-way or reciprocal, with a
  configurable combination of both directions.
- **Persistence format is selectable:** JSON or binary, detected on load.
- **Test data lives outside the repository** in
  [recoant-data](https://github.com/breskos/recoant-data); unit tests use
  small hand-made fixtures.
- **Server later.** A server with an HTTP API wraps the library and reads
  the same configuration (v0.13).
