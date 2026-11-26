# Dating and people discovery

**People who match your taste, only when both of you fit each other's age range and live nearby. Nobody you passed comes back, and a deleted account is gone everywhere at once.**

```sh
go run ./examples/usecases/dating
```

```
Mia, 29, Berlin, looks for 27–35, liked three outdoor people and passed on Pat (p27).

Discover for Mia (mutual age range, within 50 km):
  1. Luca    31, Berlin    10 km  looks for 27–36  likes hiking, climbing, cycling
  2. Eli     31, Berlin     6 km  looks for 27–36  likes hiking, climbing, cycling
  3. Robin   30, Berlin    13 km  looks for 26–35  likes hiking, climbing, cycling
  ...

After Luca (p198) deleted the account:
  1. Eli     31, Berlin     6 km  looks for 27–36  likes hiking, climbing, cycling
  ...

Export of Mia's profile without sensitive attributes: [interests max_age min_age]
Retention a year later drops 9662 old swipes.
```

Every suggestion is between 27 and 35, as Mia wants. Mia's age of 29 is also inside each suggested person's own range. Everyone is within 50 km.

## How it is built

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user` with `age`, `min_age`, `max_age`, `location`, `interests` | Age and location are marked `sensitive` |
| Relation | `likes`: user → user, half-life 60 days | People rate people. Old swipes fade. |
| Signals | `view` 0.5, `like` 5, `message` 8, `pass` (negative, 3), `block` (negative, 50) | A block outweighs everything |
| Models | `attraction` (ALS), `popular` (fallback for new members) | ALS learns taste from who likes whom |
| Serving | `discover` with two filters | See below |

The filters are what make it a dating app:

```json
"filters": [
  {"attribute": "age", "within": ["min_age", "max_age"], "mutual": true},
  {"attribute": "location", "max_distance_km": 50}
]
```

- **`within` + `mutual`:** the candidate's `age` must lie within the subject's `min_age`–`max_age`, and with `mutual` the subject's age must also lie within the candidate's range.
- **`max_distance_km`** without a point uses the subject's own `location`.

## Design decisions worth copying

- **Passes and blocks are negative feedback with `no_decay`.** They never fade, so someone you passed never comes back, even after a year.
- **Deleting an account is one call:** `engine.Delete(ref)`. The person disappears from every feed immediately, together with their attributes. You do not have to wait for the next training.
- **Retention:** `"privacy": {"retention": {"likes": "365d"}}` drops swipes a year after the last signal between two people. `engine.Expire(now)` runs it.
- **Sensitive attributes** (age, location) are used for filtering, but `ExportAttributes` leaves them out. Error messages never contain attribute values.
- **Extra filters per request**, such as "only people who like hiking" from the app's UI, are added as a `recoant.Condition` without changing the configuration.

## Make it yours

- Add `gender` and `looking_for` as categories with `same`, or `in` from the user's settings.
- Superlikes, messages and matches are just more signals with higher weights.
- For professional networking, rename the signals (`connect`, `endorse`) and keep the filters.

## Coming in later versions

v0.6 brings real **reciprocal matching**: a score for "would they like me back?", combined from both directions, plus exposure balancing so that popular profiles are not shown to everyone. Until then, `mutual` filters guarantee that the hard preferences fit both ways.
