// Package validate provides a thin wrapper around go-playground/validator
// with a singleton validator instance for use across eino-ext components.
//
// The error messages produced by Struct are tailored to be read by an LLM
// agent invoking a tool: each violated constraint is reported as a short,
// prescriptive sentence that names the JSON parameter, the offending value,
// the expected constraint, and how to fix it, so the model can adapt its
// arguments and retry.
package validate

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/go-playground/validator/v10"
)

var (
	once sync.Once
	v    *validator.Validate
)

func get() *validator.Validate {
	once.Do(func() {
		v = validator.New()
		// Report fields by their JSON name (the same name the LLM sends in a
		// tool call) instead of the Go struct field, so error messages refer
		// to parameters the caller actually recognizes.
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			tag := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if tag == "" || tag == "-" {
				return fld.Name
			}
			return tag
		})
	})
	return v
}

// Struct validates the given struct using the shared validator instance.
//
// The returned error wraps a human/LLM-readable description of every violated
// field constraint, prefixed with the validated struct's type name.
func Struct(s any) error {
	if err := get().Struct(s); err != nil {
		return errors.Wrapf(formatValidationErrors(err), "invalid parameters for %T", s)
	}
	return nil
}

// formatValidationErrors turns a validator error into a single '; '-joined
// sentence describing each violation in a way an LLM can act on.
func formatValidationErrors(err error) error {
	// validator returns *validator.InvalidValidationError when the argument is
	// not a struct; keep that message as-is.
	if _, ok := err.(*validator.InvalidValidationError); ok {
		return err
	}
	verrs, ok := err.(validator.ValidationErrors)
	if !ok || len(verrs) == 0 {
		return err
	}
	parts := make([]string, 0, len(verrs))
	for _, fe := range verrs {
		parts = append(parts, formatFieldError(fe))
	}
	return errors.New(strings.Join(parts, "; "))
}

// formatFieldError builds a prescriptive message for a single field error.
func formatFieldError(fe validator.FieldError) string {
	name := fe.Field()
	if name == "" {
		name = fe.StructField()
	}
	tag := fe.Tag()
	param := fe.Param()

	switch tag {
	case "required":
		return fmt.Sprintf("parameter '%s' is required but was not provided; supply a non-empty value and retry", name)
	case "required_if", "required_unless", "required_with", "required_without", "required_with_all", "required_without_all":
		return fmt.Sprintf("parameter '%s' is required for this request; supply a non-empty value and retry", name)
	case "oneof":
		return fmt.Sprintf("parameter '%s' (value: %s) must be one of: %s; change it to an allowed value and retry", name, quoteValue(fe), strings.ReplaceAll(param, " ", ", "))
	case "min":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain at least %s item(s); provide more and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must be >= %s; increase it and retry", name, quoteValue(fe), param)
	case "max":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain at most %s item(s); reduce it and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must be <= %s; reduce it and retry", name, quoteValue(fe), param)
	case "gt":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain more than %s item(s); provide more and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must be > %s; increase it and retry", name, quoteValue(fe), param)
	case "gte":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain at least %s item(s); provide more and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must be >= %s; increase it and retry", name, quoteValue(fe), param)
	case "lt":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain fewer than %s item(s); reduce it and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must be < %s; reduce it and retry", name, quoteValue(fe), param)
	case "lte":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain at most %s item(s); reduce it and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must be <= %s; reduce it and retry", name, quoteValue(fe), param)
	case "len":
		if isLengthKind(fe.Kind()) {
			return fmt.Sprintf("parameter '%s' (length: %s) must contain exactly %s item(s); adjust it and retry", name, lengthOf(fe), param)
		}
		return fmt.Sprintf("parameter '%s' (value: %s) must have length exactly %s; adjust it and retry", name, quoteValue(fe), param)
	case "ne":
		return fmt.Sprintf("parameter '%s' (value: %s) must not equal %s; change it and retry", name, quoteValue(fe), param)
	case "eq":
		return fmt.Sprintf("parameter '%s' (value: %s) must equal %s; change it and retry", name, quoteValue(fe), param)
	case "url":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid URL; fix it and retry", name, quoteValue(fe))
	case "uri":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid URI; fix it and retry", name, quoteValue(fe))
	case "uuid":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid UUID; fix it and retry", name, quoteValue(fe))
	case "uuid4":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid UUID v4; fix it and retry", name, quoteValue(fe))
	case "email":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid email address; fix it and retry", name, quoteValue(fe))
	case "ip":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid IP address; fix it and retry", name, quoteValue(fe))
	case "ipv4":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid IPv4 address; fix it and retry", name, quoteValue(fe))
	case "ipv6":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid IPv6 address; fix it and retry", name, quoteValue(fe))
	case "datetime":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid datetime matching layout %s; fix it and retry", name, quoteValue(fe), param)
	case "duration":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a valid Go duration string (e.g. '30s', '5m'); fix it and retry", name, quoteValue(fe))
	case "boolean":
		return fmt.Sprintf("parameter '%s' (value: %s) must be a boolean; fix it and retry", name, quoteValue(fe))
	case "alpha":
		return fmt.Sprintf("parameter '%s' (value: %s) must contain only letters; fix it and retry", name, quoteValue(fe))
	case "alphanum":
		return fmt.Sprintf("parameter '%s' (value: %s) must contain only letters and digits; fix it and retry", name, quoteValue(fe))
	case "numeric":
		return fmt.Sprintf("parameter '%s' (value: %s) must be numeric; fix it and retry", name, quoteValue(fe))
	case "excludes":
		return fmt.Sprintf("parameter '%s' (value: %s) must not contain %s; remove it and retry", name, quoteValue(fe), param)
	case "contains":
		return fmt.Sprintf("parameter '%s' (value: %s) must contain %s; adjust it and retry", name, quoteValue(fe), param)
	case "startswith":
		return fmt.Sprintf("parameter '%s' (value: %s) must start with %s; adjust it and retry", name, quoteValue(fe), param)
	case "endswith":
		return fmt.Sprintf("parameter '%s' (value: %s) must end with %s; adjust it and retry", name, quoteValue(fe), param)
	}

	// Generic fallback: keep the constraint visible so the LLM can reason about it.
	if param != "" {
		return fmt.Sprintf("parameter '%s' (value: %s) failed validation '%s=%s'; adjust it and retry", name, quoteValue(fe), tag, param)
	}
	return fmt.Sprintf("parameter '%s' (value: %s) failed validation '%s'; adjust it and retry", name, quoteValue(fe), tag)
}

// isLengthKind reports whether a kind is validated by length/count for the
// min/max/len family (string, slice, array, map). Numeric kinds are validated
// by value.
func isLengthKind(k reflect.Kind) bool {
	switch k {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return true
	default:
		return false
	}
}

// quoteValue renders the offending value for an error message. Strings are
// quoted so empty/blank values stay visible; nil reads as "<empty>".
func quoteValue(fe validator.FieldError) string {
	v := fe.Value()
	if v == nil {
		return "<empty>"
	}
	if k := fe.Kind(); k == reflect.String {
		return strconv.Quote(fmt.Sprintf("%v", v))
	}
	return fmt.Sprintf("%v", v)
}

// lengthOf renders the length/count of a string, slice, array or map value.
func lengthOf(fe validator.FieldError) string {
	v := fe.Value()
	if v == nil {
		return "0"
	}
	switch fe.Kind() {
	case reflect.String:
		return strconv.Itoa(len(fmt.Sprintf("%v", v)))
	case reflect.Slice, reflect.Array, reflect.Map:
		rv := reflect.ValueOf(v)
		return strconv.Itoa(rv.Len())
	default:
		return fmt.Sprintf("%v", v)
	}
}
