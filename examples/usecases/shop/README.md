# Online shop

**A start page that knows each customer, "customers also bought" on every product page, bestsellers for new visitors, and returns that count against a product.**

```sh
go run ./examples/usecases/shop
```

```
Mia bought trail shoes (5 stars) and running socks, returned the GPS watch and looked at an espresso pot.

For you, Mia:
  1. Water bottle        19 €
  2. Head lamp           35 €
  3. Rain jacket         89 €
  4. Stand mixer        399 €
  5. Cutting board       25 €

For you, up to 50 €:
  1. Water bottle        19 €
  2. Head lamp           35 €
  3. Cutting board       25 €
  ...

Customers who bought the cast-iron pan also bought:
  1. Cutting board       25 €
  2. Chef's knife        89 €
  ...

A new visitor sees the bestsellers:
  1. Comic                9 €
  ...
```

Mia gets the running gear she does not own yet. The returned watch never comes back, and the espresso pot she looked at brings in some kitchen products.

## How it is built

All of it is declared in [config.json](config.json). The Go code only feeds events and asks questions.

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user`, `product` with `category` and `price` | Attributes make filters like "up to 50 €" possible |
| Relation | `interacts`: user → product | One relation is enough for a shop |
| Signals | `view` 1, `cart` 5, `purchase` 10, `stars` (rating 1–5, neutral 3), `return` (negative, 8) | The weight says how much an action reveals. A purchase says more than a view. |
| Models | `taste` (ALS), `similar` (item-kNN), `bestsellers` (popularity, half-life 7 days) | One model per question |
| Serving | `for_you`, `also_bought`, `bestsellers` | Named entry points the shop calls |

Three lines of Go answer the three questions:

```go
engine.Recommend(ctx, "for_you", recoant.U("mia"), 0)                       // start page
engine.Recommend(ctx, "for_you", recoant.U("mia"), 0, recoant.Condition{     // with a price limit from the UI
	Attribute: "price", Max: &budget})
engine.Recommend(ctx, "also_bought", recoant.Ref{Kind: "product", ID: id}, 0) // product page
```

## Design decisions worth copying

- **Returns are negative feedback.** A returned product is never recommended to that customer again, and it pulls similar products down.
- **`exclude_seen`** keeps bought items off the start page. Leave it off for consumables that people buy again.
- **`fallback`** answers for visitors without history. Nobody sees an empty page.
- **Filters come from the request.** Price, brand or size from the shop's UI go into `Recommend` as conditions. You do not need a model per filter.
- **The bestsellers have a half-life of 7 days**, so they show what sells now, not what sold best last year.

## Make it yours

- Add your own signals (`wishlist`, `share`, `dwell` with a `saturate` transform) and weight them by how strong a signal of interest they are.
- Add `brand`, `size` or `in_stock` as attributes and filter on them.
- Call `engine.Delete(ref)` when a product is discontinued. It disappears from every recommendation at once.
- [evaluate](../../evaluate) shows how to measure which weights and models work best on your data.

## Coming in later versions

Bundles and "complete the look" (v0.11, hybrids and re-ranking), session-based "you just looked at" (v0.8, streaming), and cold start for brand-new products from their attributes (v0.10).
