# Measurements

How good and how fast recoant is, measured on the synthetic data sets of
[recoant-data](https://github.com/breskos/recoant-data). Every number on
this page comes from a program in [examples](../examples); the raw output
of each run is stored next to this page, so every claim can be checked and
repeated.

| Run | Date | recoant | recoant-data | Machine | Raw output |
| --- | --- | --- | --- | --- | --- |
| v0.5 | 2026-11-26 | v0.5.0 (measured on its feature branch; afterwards only tests and two behavior-preserving refactors changed) | `c8e105f` | Apple M1, 8 cores, 8 GB RAM, macOS, Go 1.27.4 | [2026-11-26](measurements/2026-11-26) |
| v0.6 (matching only) | 2026-11-26 | v0.6.0 (measured on its feature branch; afterwards only requests in other directions were changed to pass through, which the measurement does not use) | `1b9a821` (merged into main as `d6f0eb4`) | same machine | [matching.txt](measurements/2026-11-26/matching.txt) |

## Summary

**Quality.** On data with real taste structure, the best models get close
to the best possible result: on taste clusters BPR reaches **94 %** and ALS
**89 %** of the way from random to the oracle, which knows the hidden
structure that generated the data. On data from latent factors ALS reaches
**64 %** — solid, with room to grow. Attribute filters double the hit rate
in people discovery (**5.0 % → 12.2 %**) and raise it by a third in a shop
(**8.7 % → 11.8 %**). On data *without* personal taste (pure popularity),
PureSVD and ALS fall far below plain popularity; use popularity or BPR
there.
Reciprocal matching **doubles the expected matches** in a dating app
(0.46 → 1.02 per 10 recommendations) and spreads attention over twice as
many profiles.

**Speed.** One 8-core machine serves **2,800 requests per second** from ALS
or BPR (p99 under 2.5 ms) and **about 200,000 per second** from the
neighborhood models (p99 0.03 ms), on 200,000 users and 50,000 items. ALS
trains on 1.5 million interactions in **8 seconds** and folds in 2,000 new
signals in **0.16 seconds**. The store needs **about 20 bytes per
interaction**: 40 million interactions fit in **0.73 GB**.

**Fixed by these measurements.** Two performance bugs showed up and were
fixed before the run above (see [Fixes](#fixes-found-by-these-measurements)):
popularity requests were 13 times slower than needed and user-kNN kept
2 GB of memory it did not use.

## How to read the numbers

### Ranking metrics

Each data set is split by time: the first 80 % of the signals train the
model, the last 20 % are the test. For every user with signals in the test
part, the model recommends the top 10 objects the user has not seen yet.

| Metric | Meaning | Example |
| --- | --- | --- |
| precision@10 | share of the 10 recommendations the user really interacted with in the test period | 0.07: on average 0.7 of 10 recommendations are hits |
| recall@10 | share of the user's test interactions that appear in the top 10 | 0.19: 19 % of what users did next was recommended |
| ndcg@10 | like recall, but hits at the top count more (1 = perfect order) | |
| hit rate | share of users with at least one hit in their top 10 | 0.54: more than every second user gets a hit |
| coverage | share of the catalog that is recommended to anyone | 0.02: only the top 2 % of items are ever shown (popularity) |

Absolute values depend on the data: a user who interacts with 4 of 2,000
items in the test period can never reach a high precision. That is why
every table has two references:

- **random** ranks the catalog at random: the floor.
- **oracle** ranks with the hidden structure of the data set: true
  popularity ranks, true clusters or true latent factors. It is the best a
  model can do from the structure; the rest of the test signals is noise
  even the oracle cannot predict.

The column **of ceiling** puts a model between the two:
`(model − random) / (oracle − random)` for precision@10. 100 % means the
model is as good as knowing the hidden truth; 0 % means it is no better
than chance.

### What it means in production

The data is synthetic with a known structure. This makes results
comparable and judgeable, but the absolute numbers will differ on real
data: real users are noisier, catalogs have more long tail, and offline
hits are not clicks. What carries over is the ranking of the models, the
distance to the ceiling, and the cost in time and memory, which depends on
the size of the data, not on its meaning.

## Quality

Program: [examples/evaluate](../examples/evaluate) — raw output
[evaluate.txt](measurements/2026-11-26/evaluate.txt).

### basic/taste-clusters — users with clear tastes

5,000 users, 2,000 items in 10 taste clusters; 85 % of a user's
interactions stay in the own cluster.

| Model | precision@10 | recall@10 | hit rate | of ceiling |
| --- | --- | --- | --- | --- |
| random | 0.0014 | 0.003 | 1.3 % | 0 % |
| popularity | 0.0142 | 0.036 | 13.3 % | 16 % |
| PureSVD | 0.0613 | 0.157 | 47.5 % | 74 % |
| user-kNN | 0.0667 | 0.170 | 49.4 % | 81 % |
| item-kNN | 0.0702 | 0.180 | 52.1 % | 85 % |
| ALS | 0.0739 | 0.190 | 53.9 % | 89 % |
| **BPR** | **0.0779** | **0.199** | **56.0 %** | **94 %** |
| oracle | 0.0825 | 0.211 | 58.2 % | 100 % |

**Verdict: very good.** Every personalized model beats popularity by a
factor of 4.3 to 5.5, and BPR and ALS are close to the best possible result.
More than half of the users find something they will use among 10
recommendations.

### latent/implicit-feedback — taste as latent factors

4,000 users, 2,000 items; users choose items by the dot product of hidden
8-dimensional factors; deeper funnel signals (purchase, long dwell) mean
stronger affinity.

| Model | precision@10 | recall@10 | hit rate | of ceiling |
| --- | --- | --- | --- | --- |
| random | 0.0021 | 0.004 | 2.1 % | 0 % |
| popularity | 0.0143 | 0.025 | 13.7 % | 14 % |
| item-kNN | 0.0297 | 0.053 | 25.8 % | 32 % |
| PureSVD | 0.0335 | 0.061 | 29.1 % | 37 % |
| user-kNN | 0.0374 | 0.066 | 31.6 % | 41 % |
| BPR | 0.0503 | 0.090 | 40.6 % | 57 % |
| **ALS** | **0.0570** | **0.102** | **44.3 %** | **64 %** |
| oracle | 0.0874 | 0.155 | 59.9 % | 100 % |

**Verdict: good, with room to grow.** ALS, which weights deep signals
higher through its confidence, is clearly best and 4 times better than
popularity. A third of the distance to the ceiling is left: more training
data per user, tuned regularization or more iterations are the levers.

### basic/zipf-popularity — no personal taste at all

2,000 users who all draw items from the same popularity distribution. Here
popularity *is* the oracle.

| Model | precision@10 | hit rate | of ceiling |
| --- | --- | --- | --- |
| random | 0.0028 | 2.8 % | 0 % |
| PureSVD | 0.0142 | 13.6 % | 13 % |
| ALS | 0.0322 | 28.1 % | 35 % |
| user-kNN | 0.0805 | 56.9 % | 92 % |
| item-kNN | 0.0840 | 59.4 % | 96 % |
| BPR | 0.0869 | 60.9 % | 99 % |
| **popularity** = oracle | 0.0876 | 60.8 % | 100 % |

**Verdict: know this limit.** Neighborhood models and BPR follow
popularity well. PureSVD and ALS do not: they model *differences* between
users and by design downweight what everybody does. On a catalog where
taste plays a small role (bestsellers, news headlines), combine them with
popularity or use BPR; a weighted hybrid is planned for v0.11.

### relations/social — whom to follow

4,000 users in 8 clusters who follow 5–30 others, 80 % in the own cluster
but otherwise at random. The oracle knows the clusters, not who will
follow whom.

| Model | precision@10 | hit rate | of ceiling |
| --- | --- | --- | --- |
| random | 0.0008 | 0.8 % | 0 % |
| user-kNN | 0.0056 | 5.4 % | 89 % |
| BPR | 0.0056 | 5.4 % | 89 % |
| PureSVD | 0.0054 | 5.4 % | 85 % |
| **ALS** | **0.0066** | **6.5 %** | **107 %** |
| oracle (clusters) | 0.0062 | 6.0 % | 100 % |

**Verdict: as good as the data allows.** The low absolute numbers come from
the data: within a cluster of 500 users, follows are random, so no model
can predict them better. ALS even slightly exceeds the cluster oracle,
because it also learns which users are followed more often.

### latent/explicit-ratings — predicting star ratings

3,000 users rate 1,500 movies with 1–5 stars, generated like the Netflix
Prize data. Measured by the error of the predicted rating (RMSE, lower is
better). According to the data set, the true model has an RMSE of 0.55
(noise and rounding), the true biases alone 0.91.

| Model | RMSE | MAE | fit |
| --- | --- | --- | --- |
| global mean | 1.017 | 0.865 | |
| Funk-SVD | 0.829 | 0.671 | 0.42 s |
| biased MF | 0.807 | 0.651 | 0.43 s |
| **SVD++** | **0.792** | **0.636** | 0.66 s |
| *true biases (data set)* | *0.91* | | |
| *true model (data set)* | *0.55* | | |

**Verdict: good.** All three models are well below the true biases, so they
learn the latent taste, not only "this movie is liked". SVD++ is best and
closes about half of the gap between the mean and the noise floor; on
average its prediction is 0.64 stars off.

### Attributes, filters, deletion and retention

Program: [examples/attributes](../examples/attributes) — raw output
[attributes.txt](measurements/2026-11-26/attributes.txt). Attribute events
and signals are replayed in time order up to 80 % of the period, accounts
and discontinued products are deleted when they are deleted, the retention
is applied, the models are trained, then recommendations are measured
without and with filters.

**attributes/people** — 2,500 users of a people-discovery app with age,
preferred age range, location and interests; likes depend on both sides'
preferences. Relevant: users the subject liked after the split.

| Serving | precision@10 | recall@10 | hit rate | coverage |
| --- | --- | --- | --- | --- |
| ALS, no filters | 0.0050 | 0.033 | 5.0 % | 0.18 |
| ALS, mutual age preferences + 50 km | **0.0124** | **0.082** | **12.2 %** | 0.39 |

**attributes/shop** — 4,000 users with a budget and preferred tags, 2,500
products with price and tags. Relevant: carts and purchases after the
split.

| Serving | precision@10 | recall@10 | hit rate | coverage |
| --- | --- | --- | --- | --- |
| ALS, no filters | 0.0089 | 0.073 | 8.7 % | 0.21 |
| ALS, budget | 0.0107 | 0.089 | 10.5 % | 0.27 |
| ALS, budget + shared tags | **0.0120** | **0.101** | **11.8 %** | 0.45 |

**Verdict: filters pay off.** Mutual filters more than double the hits in
people discovery, because a like needs both sides' preferences, which a
one-sided model cannot see. In the shop, the budget filter removes
products users would not buy. Filters also spread recommendations over
more of the catalog (coverage).

The program also checks the rules and fails if one is broken; all held:

- no deleted account or discontinued product was recommended, also not by
  trained models before their next training;
- every filtered recommendation meets its filters, checked independently
  of the filter code;
- the retention of 60 days dropped exactly the expected 14,889 of 65,094
  pairs;
- no sensitive attribute (age, gender, location) was exported.

### matching/dating — reciprocal matching

Program: [examples/matching](../examples/matching) — raw output
[matching.txt](measurements/2026-11-26/matching.txt). 3,000 users of a
dating app, each with a hidden style and a preferred style in others;
attractive users get most likes and are pickier (the most attractive tenth
receives 28 % of all likes but returns only 6.6 % of them, the others
16.9 %). Trained on the first 80 % of the period; every user with a like
afterwards gets 10 profiles they have not seen. ALS with 16 factors and
α = 10 serves all four variants.

The main number is **expected matches**: for each recommended pair, the
true probability that both like each other, known from the generator,
summed over the 10 recommendations. It says how many matches a user would
get if they looked at the 10 profiles. The logged mutual hit rate (the
user liked the profile later and the profile liked the user) is shown
too, but it is small and noisy, because a match in the log also needs both
to have come across each other by chance.

| Recommender | expected matches per 10 | of ceiling | hit rate (one-way) | mutual hit rate | distinct profiles | Gini | ms/request |
| --- | --- | --- | --- | --- | --- | --- | --- |
| random (floor) | 0.35 | 0 % | 1.3 % | 0.26 % | 2,944 | 0.48 | — |
| one-way ALS | 0.46 | 3 % | **4.7 %** | 0.36 % | 994 | 0.71 | 0.13 |
| reciprocal (harmonic) | 0.99 | 15 % | 2.4 % | **0.87 %** | 2,198 | 0.53 | 0.27 |
| reciprocal + exposure balancing 0.5 | **1.02** | **15 %** | 1.7 % | 0.73 % | 2,304 | **0.46** | 0.28 |
| oracle (ceiling) | 4.71 | 100 % | 3.8 % | 2.15 % | 1,702 | 0.54 | — |

**Verdict: reciprocal matching doubles the matches.** One-way ALS finds
the profiles a user likes (the highest one-way hit rate), but many of
them are attractive profiles that rarely like back, so it is hardly better
than random at producing matches. Combining both directions gives **2.1
times the expected matches** and **2.4 times the logged matches**, and
spreads attention over twice as many profiles (Gini 0.71 → 0.53). Exposure
balancing spreads it further (Gini 0.46) at no cost in matches. The price
is a lower one-way hit rate: users see fewer of the profiles they would
like and more of those who would like them back — the right trade for
dating, mentoring or job matching, the wrong one for "follow" or discovery
(use `mode: one_way` there). A request costs twice as long (0.27 ms),
because the reverse direction is scored for 200 candidates.

**Room to grow.** 15 % of the ceiling is honest but modest: the oracle
knows the exact formula behind every like. The learned interests are the
limit — implicit ALS still favors popular profiles in both directions — not
the combination: rescoring 1,000 or all 3,000 candidates instead of 200
changed the result by less than 2 %. Attribute filters (see above) and
feature models (v0.10) are the next levers.

**Collective factorization** (shared subject vectors across relations) is
covered by unit tests; no data set with two relations of the same users
exists yet, so it is not measured here.

## Performance

Program: [examples/benchmark](../examples/benchmark) — raw output
[benchmark.txt](measurements/2026-11-26/benchmark.txt) and
[scale.txt](measurements/2026-11-26/scale.txt).

Data: **benchmark/large**, 3.5 million signals (visit, click, dwell, cart,
purchase) of **200,000 users** on **50,000 items**, which the store
aggregates into **1.48 million** user–item interactions. All but the last
2,000 signals train the models; the last 2,000 arrive as new data for the
refresh. Latency: 2,000 users, top 10, excluding seen items, one request at
a time; throughput: the same requests from 8 goroutines.

### Ingestion and memory of the store

| Measurement | Result |
| --- | --- |
| Ingestion | **2.6 million signals per second** (one goroutine) |
| Store memory, benchmark/large | 34 MB for 1.48 million interactions (23 bytes per interaction) |
| Store memory at scale | **0.73 GB** for 40 million signals = 36 million interactions of 2 million users on 500,000 items (20 bytes per interaction, IDs included); ingested at 2.0 million signals per second |

**Meaning:** ingestion is not a bottleneck; a stream of thousands of
signals per second uses a fraction of one core. As a rule of thumb, plan
**2 GB of memory per 100 million interactions** for the store, plus the
models below.

### Models

| Model | Training | Model memory | Refresh after 2,000 new signals | p50 | p99 | Throughput, 8 cores |
| --- | --- | --- | --- | --- | --- | --- |
| popularity | 0.44 s | 3 MB | online, per signal | 0.92 ms | 1.02 ms | 5,200 req/s |
| item-kNN | 0.17 s | 18 MB | 0.13 s | 0.03 ms | 0.03 ms | 195,000 req/s |
| user-kNN | 4.4 s | 101 MB | 0.17 s | 0.02 ms | 0.03 ms | 218,000 req/s |
| PureSVD, 64 factors | 7.8 s | 156 MB | full training | 3.1 ms | 3.4 ms | 1,650 req/s |
| ALS, 32 factors | 8.2 s | 66 MB | 0.16 s | 1.8 ms | 1.8 ms | 2,800 req/s |
| BPR, 32 factors | 19.4 s | 66 MB | full training | 1.8 ms | 2.5 ms | 2,800 req/s |

**Meaning:**

- **Latency is low for all models.** Even the slowest p99 (PureSVD,
  3.4 ms) is far below the budget of a web request. The neighborhood
  models answer in microseconds because they only look at the neighbors of
  the user's history.
- **Throughput of the factor models is bounded by scoring the whole
  catalog:** every request computes 50,000 dot products. 2,800 requests
  per second on one machine are enough for most applications; catalogs of
  millions of items need the approximate nearest neighbor search planned
  for v0.12.
- **Fresh recommendations without retraining.** ALS and the kNN models
  update in 0.13–0.17 s after 2,000 new signals, so new behavior can be
  reflected every few seconds. PureSVD and BPR need a full training; at
  8–19 s this is fine every few minutes to hours at this size.
- **Memory of the factor models** is (users + items) × factors × 8 bytes:
  66 MB for ALS here, 1.3 GB for 5 million users with 32 factors.

### Fixes found by these measurements

The first run of the benchmark
([benchmark-before-fixes.txt](measurements/2026-11-26/benchmark-before-fixes.txt))
showed two problems, which were fixed in v0.5:

| Problem | Before | After |
| --- | --- | --- |
| Popularity asked the store for every one of the 50,000 candidates whether the user had seen or rejected it, each time under a lock; 8 cores blocked each other. Now the user's history is read once per request. | p50 11.8 ms, 82 req/s | p50 0.92 ms, **5,200 req/s** (63×) |
| kNN kept the full sorted similarity list of every row (often thousands of entries) behind its top 50. Now only the top 50 are kept. | user-kNN 2,006 MB, refresh 0.68 s; item-kNN 46 MB | user-kNN **101 MB**, refresh **0.17 s**; item-kNN 18 MB |

Popularity is the fallback of most configurations, so the first fix
matters for every new user.

## Limits of these measurements

- **Synthetic data.** It has a known structure, which makes results
  judgeable, but real data is noisier; expect lower absolute precision and
  compare models by their ranking and distance to the ceiling.
- **Offline metrics.** A hit means the user interacted with the item later
  anyway; it does not measure whether a recommendation *caused* the
  interaction. Online A/B tests remain necessary.
- **One machine, one run.** Times vary by a few percent between runs and
  depend on the CPU; the Apple M1 has 4 performance and 4 efficiency cores.
  Throughput was measured in one process without network or serialization.
- **Default parameters.** The models ran with the parameters in the
  programs; tuning can change the quality results.

## Reproduce

Clone both repositories next to each other, then from the recoant folder:

```bash
go run ./examples/evaluate -data ../recoant-data/synthetic
```

```bash
go run ./examples/attributes -data ../recoant-data/synthetic
```

```bash
go run ./examples/matching -data ../recoant-data/synthetic
```

```bash
go run ./examples/benchmark -data ../recoant-data/synthetic
```

```bash
go run ./examples/benchmark -scale
```

Both ran on the 8 GB machine above. Run them on an otherwise idle machine;
other load changes the times. When you add a run, put its raw
output in a new folder under [measurements](measurements) and add a row to
the table at the top.
