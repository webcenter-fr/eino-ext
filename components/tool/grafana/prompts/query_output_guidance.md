** How to interpret the output (IMPORTANT) **
- `seriesCount` is the TRUE number of series/streams the query returned.
  - `seriesCount == 0` means the query returned NO data. This is a normal,
    successful result — NOT an error. It means the query is too restrictive or
    the metric/stream does not exist. Use `hints` and adjust the query.
  - A large `seriesCount` (e.g. hundreds or thousands) means the query is too
    BROAD. Narrow it by adding label filters (e.g. `{app="checkout", env="prod"}`
    for LogQL, or `metric{label="value"}` for PromQL).
- `truncated` is true when `series` was capped at `maxSeries` (default 20). The
  `series` array is only a SAMPLE of the full result — `seriesCount` is the real
  total.
- `series[].labels` are the label sets of each series. Inspect them to choose
  which label values to filter on when narrowing a broad query.
- `resultType` is `vector` (instant metrics), `matrix` (range metrics),
  `streams` (Loki log queries), `scalar`, or `string`.
- Prefer instant queries (`queryType: "instant"`) to check cardinality and
  no-data. Use `queryType: "range"` only when you actually need a time series.
