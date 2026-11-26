# Examples

Runnable example programs. They are excluded from the coverage gate in CI.

| Example | Purpose |
| --- | --- |
| [config](config) | Example configurations: a shop (implicit signals, star ratings, returns) and a people-discovery app (likes and passes between users) |
| [evaluate](evaluate) | Compare all recommenders (ranking metrics) and the matrix factorizations (rating RMSE) on the synthetic data sets of [recoant-data](https://github.com/breskos/recoant-data): `go run ./examples/evaluate -data ../recoant-data/synthetic` |
