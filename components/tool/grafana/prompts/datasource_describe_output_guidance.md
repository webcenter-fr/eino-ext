** How to use the output (IMPORTANT) **
- The `uid` and `type` fields are what you need to reference this data source in
  a dashboard panel's `datasource` object (e.g. `{"type":"prometheus","uid":"..."}`).
- `jsonData` holds plugin-specific config (e.g. `timeField`, `timeInterval`,
  `region`, `database`) useful for writing queries. Sensitive sub-fields are
  redacted to "<redacted>" and must not be sent back in any request.
- Sensitive top-level fields (passwords, tokens) are excluded entirely.
