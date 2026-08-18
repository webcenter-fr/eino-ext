** How to interpret the output (IMPORTANT) **
- Each panel gets a `verdict`:
  - `ok` — at least one query returned data within the series limit.
  - `no-data` — every executed query returned zero series (empty result).
  - `too-many-series` — a query returned more series than `maxSeriesPerPanel`.
  - `error` — a query failed (bad PromQL/LogQL, missing datasource, auth, ...).
  - `skipped` — the panel was not executed (unsupported datasource type, no
    datasource, no queries, or beyond `maxPanels`). The `reason` explains why.
- The `summary` object rolls up the panel verdict counts; `validatedPanels` is
  the number of panels that were actually executed (all non-`skipped` panels).
- Each executed query has its own `verdict`, `seriesCount`, and `sampleLabels`
  (up to `maxSeriesSample` label sets) to help you diagnose the specific query.

** Recommended workflow **
1. Create or update a dashboard with `grafana_dashboard_write`.
2. Run `grafana_dashboard_validate` with the dashboard UID.
3. Fix panels with `no-data` (widen query) or `too-many-series` (narrow query
   with label filters) by editing the dashboard model.
4. Save again and re-validate until all panels are `ok`.
