package grafana

import "strings"

// redactedPlaceholder replaces sensitive values in redacted output.
const redactedPlaceholder = "<redacted>"

// sensitiveKeyFragments are case-insensitive substrings; any JSON key whose
// lowercased form contains one of these is treated as secret and redacted.
// Curated for Grafana datasource jsonData and custom HTTP headers: catches
// password, basicAuthPassword, secret, clientSecret, secretAccessKey, token,
// accessToken, refreshToken, authToken, privateKey, apiKey, api_key,
// api-key, X-Api-Key, httpHeaderValue, httpHeaderValue1..N, customHttpHeaders,
// accessKey, accessKeyId, sigV4AccessKey, credential, authorization, bearer.
//
// Over-redaction is intentional and safe: a "<redacted>" placeholder is
// preferable to leaking a secret. Benign dashboard-building keys
// (timeField, database, region, maxLines, timeInterval, authMode,
// oauthPassThru) do NOT contain any of these fragments and are preserved.
var sensitiveKeyFragments = []string{
	"password", "secret", "token", "privatekey", "apikey",
	"api-key", "api_key", "accesskey", "httpheader", "credential",
	"authorization", "bearer",
}

// sensitiveKeyExact are case-insensitive exact key names treated as secret.
// Kept small and precise to avoid over-redaction (e.g. "authMode" is NOT
// matched here because only the exact key "auth" is).
var sensitiveKeyExact = map[string]bool{
	"auth": true,
	"pass": true,
	"pwd":  true,
}

// isSensitiveKey reports whether key names a secret. Matching is
// case-insensitive: exact match against sensitiveKeyExact, or substring match
// against sensitiveKeyFragments. Over-redaction is intentional and safe here
// (a redacted placeholder is preferable to leaking a secret).
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	if sensitiveKeyExact[lower] {
		return true
	}
	for _, frag := range sensitiveKeyFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// redactSensitiveJSON recursively walks v and replaces the values of
// sensitive keys (case-insensitive) with redactedPlaceholder. Maps and slices
// are copied; scalars are returned unchanged. A nil map becomes an empty
// non-nil map; call redactedJSONData to preserve nilness for typed inputs.
func redactSensitiveJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if isSensitiveKey(k) {
				out[k] = redactedPlaceholder
			} else {
				out[k] = redactSensitiveJSON(vv)
			}
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = redactSensitiveJSON(vv)
		}
		return out
	default:
		return v
	}
}

// redactedJSONData is a typed convenience wrapper for map[string]any inputs
// (the dataSource.JSONData field). Returns nil for a nil input so the output
// struct's omitempty drops it.
func redactedJSONData(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	r, _ := redactSensitiveJSON(m).(map[string]any)
	return r
}
