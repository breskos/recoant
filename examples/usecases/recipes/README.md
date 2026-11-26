# Recipes

**Star ratings that count in both directions, "ready in 30 minutes" as a fixed filter, "vegetarian" from the user's settings, and similar recipes.**

```sh
go run ./examples/usecases/recipes
```

```
Anna rated pad thai and ramen with 5 stars, the sushi bowl with 4 and the schnitzel with 1.

Recipes for Anna:
  1. Vegetable curry    asian     35 min  vegetarian, vegan
  2. Fried rice         asian     15 min  vegetarian
  3. Tofu stir-fry      asian     20 min  vegetarian, vegan
  4. Tacos al pastor    mexican   60 min
  5. Risotto ai funghi  italian   40 min  vegetarian

Tonight, ready in 30 minutes:
  1. Fried rice         asian     15 min  vegetarian
  ...

Cooks who made guacamole also made:
  1. Bean burrito       mexican   20 min  vegetarian
  ...
```

Anna gets Asian dishes. German dishes do not appear: her one-star schnitzel counts against them.

## How it is built

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user`, `recipe` with `cuisine`, `diet` (set), `minutes` | A set holds several tags: vegetarian *and* vegan |
| Relation | `cooks`, rating channel with `aggregate: last` | A new rating replaces the old one |
| Signals | `open` 1, `save` 4, `cooked` 8, `stars` (rating 1–5, neutral 3, weight 6) | |
| Models | `taste` (ALS), `similar` (item-kNN), `popular` | |
| Serving | `for_you`, `tonight` (filter: `minutes` ≤ 30), `similar` | |

```json
"stars": {"relation": "cooks", "feedback": "rating", "scale": [1, 5], "neutral": 3, "weight": 6}
```

A rating counts as `weight × (stars − neutral)`: 5 stars gives +12, 3 stars gives 0, 1 star gives −12. Cooking a recipe (+8) and rating it one star therefore adds up to a dislike.

## Design decisions worth copying

- **Choose the neutral point deliberately.** At 3, ratings of 1–2 count down. If your users rarely give fewer than 4 stars, set it to 4.
- **Weight ratings strongly enough** that they can outweigh the action they rate. Otherwise "cooked, hated it" still counts as a like.
- **Fixed filters belong in the configuration** (`tonight`). **Personal filters belong in the request** (diet, allergies): `recoant.Condition{Attribute: "diet", In: []string{"vegetarian"}}`.
- **`aggregate: last`** for ratings: if Anna re-rates, the new rating counts, not the sum.

## Make it yours

- Allergies as a set, and ingredients as a set for "with what is in my fridge".
- `minutes` and `difficulty` as numbers, `season` as a category.
- The same pattern works for wine, books or restaurants: anything with star ratings and tags.

## Coming in later versions

Taste profiles and segments ("the quick weeknight cooks", v0.7), diversity across cuisines in the list (v0.11), and new recipes recommended from their ingredients before anyone has cooked them (v0.10).
