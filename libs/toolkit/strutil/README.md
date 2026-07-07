# strutil — String helpers

`strutil` provides small string helpers shared across components: length-capped
truncation with a marker, and extraction of a JSON block from free-form LLM
output.

## Functions

```go
func Truncate(s string, maxLen int, marker string) string
func StripMarkdownFences(s string) string
func ExtractJSONBlock(content string) string
```

- `Truncate` — returns `s` unchanged when within `maxLen` (or `maxLen <= 0`);
  otherwise returns the first `maxLen` bytes followed by `marker`.
- `StripMarkdownFences` — removes a surrounding ```` ```lang ... ``` ```` fenced
  code block, returning the trimmed inner content.
- `ExtractJSONBlock` — strips markdown fences, then returns the first JSON array
  or object found via outermost bracket matching (empty string when none).

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"

short := strutil.Truncate(content, 2000, "...")
jsonText := strutil.ExtractJSONBlock(llmOutput)
```
