# Customer segments

**Who are my customers? recoant groups them into segments from what they buy and who they are, describes every segment, assesses each customer, and places new customers right away.**

```sh
go run ./examples/usecases/customers
```

```
Who are my customers? 4 segments from what they buy and their age:
  s0  100 customers (25 %)  age 30–42  buys garden, household       7.7 products each  premium 30 %
  s1  101 customers (25 %)  age 32–45  buys electronics, games      7.5 products each  premium 30 %
  s2  100 customers (25 %)  age 63–80  buys garden, household       7.7 products each  premium 29 %
  s3  100 customers (25 %)  age 18–25  buys electronics, games      7.2 products each  premium 27 %

Tom, 38: segment s1, typical for it (distance 1.46), 4 products: 3 liked, 1 returned; most similar: c313, c289, c389
Recommended for Tom: Webcam, Puzzle game, Lunch boxes
Customers like Tom: c313 (s1), c289 (s1), c201 (s1)

New customer Lea, 21, viewed a puzzle game: segment s3
```

The four segments are the four real groups of this shop:
- **Families** (s0) and **retirees** (s2) buy the same garden and household products.
- **Techies** (s1) and **students** (s3) buy the same games and electronics.
- **Age** is what tells the members of each pair apart.

That is why the segments use both features.

## How it is built

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user` with `age` (sensitive), `city`, `membership`; `product` with `category` | |
| Relation | `buys`: user → product | |
| Signals | `view` 1, `purchase` 5, `return` (negative, 5) | Returns show up in the profiles as negative interactions |
| Model | `taste` (ALS) | Its taste vectors describe what each customer buys |
| Segments | `user`: 4 segments on `taste` and `age` | See below |
| Serving | `for_you`, `people_like_me` (filter: same segment) | |

```json
"segments": {
  "user": {"k": 4, "features": ["taste", "age"], "model": "taste", "seed": 1, "allow_sensitive": true}
}
```

- **`features`:** `taste` and `appearance` are the vectors of a factor model (`model`). Attribute names cluster on attributes. Categories and sets are one-hot, numbers and locations standardized. Every feature counts equally unless `weights` say otherwise.
- **`allow_sensitive`:** age is sensitive, so clustering on it is an explicit decision.
- **What training does:** `engine.Fit` also fits the segments and writes every customer's segment into the attribute `segment` (`s0`, `s1`, …). That attribute is exactly what the filter `{"attribute": "segment", "same": true}` uses.

What you get:

```go
seg, _ := engine.Segmenter(recoant.User)
seg.Profiles()               // per segment: size, share, typical products, attribute distributions, activity, negative share
seg.Profile(customer, 3)     // per customer: segment, how typical, liked and rejected, 3 most similar customers
seg.Segment(newCustomer)     // the nearest segment, also for customers who were not part of training
```

## Design decisions worth copying

- **Cluster on what matters, and only on that.** A feature without information destroys segments. Adding the city (which has no influence here) to taste on the [customers data set](../../../docs/measurements.md#segmentscustomers--which-features-make-segments) dropped the purity from 0.67 to 0.22. Taste and age together reach 0.91.
- **Combine behavior and attributes.** Behavior alone merges groups that buy the same things. Attributes alone merge groups of the same age.
- **Choose k by the decisions you want to make,** such as four campaigns or four onboarding flows, then read the profiles and name the segments.
- **Segment numbers stay stable.** After each refit, every segment keeps the number of the nearest old segment, so `s1` stays the techies and stored segment attributes keep their meaning.
- **New customers are placed immediately.** Their features decide; with few signals, attributes like age carry them.

## Make it yours

- Use campaigns per segment: the `Typical` products of a profile are a ready-made "popular in your group" list.
- Use `evaluation.BySegment` to check whether one segment is served worse than the others.
- Use `appearance` to segment products: groups of products bought by the same people.

## Coming in later versions

Funnel stages in profiles (v0.9), segments as features of models for new customers (v0.10), and explanations such as "popular with customers like you" (v0.11).
