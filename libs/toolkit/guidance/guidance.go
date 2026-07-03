package guidance

import (
	"fmt"
	"strings"
)

type ListField struct {
	Name        string
	Description string // e.g. "Set `namespace` whenever you know it."
}

func List(toolName string, fields ...ListField) string {
	var b strings.Builder
	b.WriteString("\n** How to limit output (IMPORTANT) **\n")
	b.WriteString(fmt.Sprintf("Always narrow the %s query to avoid large responses:\n", toolName))
	for _, f := range fields {
		b.WriteString(fmt.Sprintf("- %s\n", f.Description))
	}
	return b.String()
}

func Describe(excludeFields ...string) string {
	var b strings.Builder
	b.WriteString("\n** How to limit output (IMPORTANT) **\n")
	b.WriteString("Use `excludeFieldsOutput` to drop large sections you do not need (any of\n")
	b.WriteString(fmt.Sprintf("'%s') instead of fetching the full resource.\n", strings.Join(excludeFields, "', '")))
	return b.String()
}
