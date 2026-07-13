package otelmetrics

import "go.opentelemetry.io/otel/attribute"

// Attrs builds an attribute.Set from key/value pairs (string values only —
// enforces the low-cardinality discipline).
func Attrs(kv ...string) attribute.Set {
	if len(kv)%2 != 0 {
		kv = kv[:len(kv)-1]
	}
	kvs := make([]attribute.KeyValue, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		kvs = append(kvs, attribute.String(kv[i], kv[i+1]))
	}
	return attribute.NewSet(kvs...)
}
