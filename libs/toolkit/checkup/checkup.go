// Package checkup provides types and helpers for component connectivity and RBAC
// diagnostics.
package checkup

import (
	"encoding/json"
	"slices"
	"strings"
)

// Status values for checkup results.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusLimited = "limited"
)

// Shared checkup user/tenant identifiers used by memory and storage probes
// so that test artifacts are easily identifiable and cleaned up.
const (
	CheckUser   = "__checkup"
	CheckConvID = "checkup_test"
)

// Result represents the outcome of a single probe.
type Result struct {
	Component string `json:"component"`
	Instance  string `json:"instance,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Results is a collection of checkup results.
type Results []Result

// OK reports true when no result has status "error".
func (r Results) OK() bool {
	return !slices.ContainsFunc(r, func(e Result) bool {
		return e.Status == StatusError
	})
}

// JSON marshals the results to a JSON string. indent is passed to json.MarshalIndent;
// pass "" for compact output or a whitespace string (e.g. "  ") for pretty-printed.
// On marshal failure it returns a JSON error string.
func (r Results) JSON(indent string) string {
	var b []byte
	var err error
	if indent == "" {
		b, err = json.Marshal(r)
	} else {
		b, err = json.MarshalIndent(r, "", indent)
	}
	if err != nil {
		return `{"error":"failed to marshal results: ` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`
	}
	return string(b)
}

// Merge combines multiple Results slices into a single flat slice.
func Merge(all ...Results) Results {
	var out Results
	for _, a := range all {
		out = append(out, a...)
	}
	return out
}

// DependencyFailed returns a Results slice where every named component has
// status "error" with the message "dependency failed".  Use this when an
// upstream probe (e.g. client creation) fails so that downstream probes
// are marked accordingly without duplicating the same error.
func DependencyFailed(names ...string) Results {
	r := make(Results, len(names))
	for i, name := range names {
		r[i] = Result{
			Component: name,
			Status:    StatusError,
			Error:     "dependency failed",
		}
	}
	return r
}
