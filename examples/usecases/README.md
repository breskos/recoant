# Use cases

**What could you build with recoant?** Seven small, runnable stories, each a complete application in about 200 lines of Go and one JSON configuration. Pick the one closest to your idea, run it, and copy its configuration as your starting point.

Each needs no data download, runs in about a second and prints the same result every time:

```sh
go run ./examples/usecases/shop
```

| You are building … | Start here | What you will see |
| --- | --- | --- |
| an **online shop**, a marketplace, a catalogue | [shop](shop) | A personal start page, "customers also bought", bestsellers for new visitors, a price filter, returns that count against a product |
| a **dating app**, people discovery, networking | [dating](dating) | Suggestions only when both people fit each other's age range and live nearby, passes that never come back, account deletion, retention, privacy |
| **music**, podcasts, video, audiobooks | [music](music) | A weekly mix that follows changing taste, skips as negative feedback, a track radio, charts, a family mode |
| **recipes**, wine, books, restaurants: anything rated with stars | [recipes](recipes) | Star ratings that count in both directions, "ready in 30 minutes", vegetarian from the user's settings, similar recipes |
| **events**, meetups, venues, courses | [events](events) | Events in my city, events near where I am right now, free only, past events disappearing |
| a **job board**, freelancing, housing: two-sided markets | [jobs](jobs) | Jobs for candidates, candidates for jobs, matching skills, salary range, distance |
| **customer insight**: segments, profiles, targeting | [customers](customers) | Segments from behavior and age, a profile of every segment and customer, "customers like me", new customers placed immediately |

## What every use case shows

Every recoant application answers the same five questions. The examples answer them differently, and that is the point:

1. **What are the things?** Kinds (`user`, `product`, `track`, `event`, `job`) and their attributes (category, number, set, geo).
2. **What does each action mean?** Signals with a weight: a purchase says more than a view, a skip or a return says "no".
3. **How fast does taste change?** A half-life: 7 days for bestsellers, 21 days for music, never for a "pass".
4. **Which question does each model answer?** "For you" (ALS), "similar to this" (item-kNN), "what is popular now" (popularity).
5. **What must always hold?** Filters: price, distance, age ranges in both directions, skills, a diet.

Each README explains the configuration of its use case, the design decisions worth copying and what to change for your own application.

## Which feature is where

| I want … | Feature | Shown in |
| --- | --- | --- |
| personal recommendations | ALS, `exclude_seen` | shop, music, recipes, jobs |
| "similar to this" | item-kNN with an entity as the subject | shop, music, recipes, events, jobs |
| something for new users | `fallback` to popularity | shop, dating, music, recipes, events |
| "what is hot right now" | popularity with a `half_life` | shop, music, events |
| a "no" to count | `feedback: negative` | shop, dating, music, events, jobs |
| star ratings | `feedback: rating` with `scale` and `neutral` | shop, recipes |
| dwell or play time as strength | `transform: saturate` | music |
| old behaviour to fade | `half_life` per relation and channel | dating, music, recipes, jobs |
| a fixed rule for everybody | `filters` in the configuration | dating, recipes, events, jobs |
| a filter from the UI | `recoant.Condition` in the request | shop, dating, music, recipes, events |
| a match both sides want | `matching`: `reciprocal`, `exposure_balancing` | dating, jobs |
| both sides' hard preferences to fit | `within` + `mutual` | dating |
| the same city or a shared skill | `same` | events, jobs |
| distance | `max_distance_km`, `near` | dating, events, jobs |
| two directions (A → B and B → A) | two relations | jobs |
| something deleted everywhere at once | `engine.Delete` | dating, events |
| old data dropped | `privacy.retention`, `engine.Expire` | dating |
| sensitive data kept out of exports | `sensitive`, `ExportAttributes` | dating |
| groups of customers and their profiles | `segments`, `Segmenter.Profiles`, `Segmenter.Profile` | customers |
| "from my segment" | filter `{"attribute": "segment", "same": true}` | customers |

## From example to application

The examples generate their own small data with a fixed seed. In your application:

- **Signals** come from your event stream: `engine.Add` for each event, or `engine.AddBatch`.
- **Attributes** come from your database, as `attr.Event` on every change.
- **Training:** `engine.Fit` once, then `engine.Refresh` regularly for new signals. Use `persist` to save and load the store, the attributes and the models.
- **Measuring:** [evaluate](../evaluate) shows how to compare models and weights on your data. [measurements](../../docs/measurements.md) shows what quality and speed to expect.

Want to see a use case that is missing? Open an issue.
