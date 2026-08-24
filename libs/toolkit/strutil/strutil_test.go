package strutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTruncate_NoTruncation(t *testing.T) {
	assert.Equal(t, "hello", Truncate("hello", 10, "..."))
	assert.Equal(t, "hello", Truncate("hello", 5, "..."))
}

func TestTruncate_ZeroOrNegativeMaxLen(t *testing.T) {
	assert.Equal(t, "hello", Truncate("hello", 0, "..."))
	assert.Equal(t, "hello", Truncate("hello", -1, "..."))
}

func TestTruncate_Truncates(t *testing.T) {
	assert.Equal(t, "he...", Truncate("hello world", 2, "..."))
}

func TestTruncate_EmptyMarker(t *testing.T) {
	assert.Equal(t, "hel", Truncate("hello world", 3, ""))
}

func TestTruncate_MultiByteUTF8(t *testing.T) {
	// "café" is 5 bytes but 4 runes. Truncating at 3 runes should give "caf",
	// not "caf" + a broken byte.
	assert.Equal(t, "caf...", Truncate("café", 3, "..."))

	// Japanese: "こんにちは" is 15 bytes but 5 runes.
	assert.Equal(t, "こんに...", Truncate("こんにちは", 3, "..."))
}

func TestTruncate_RuneCountEqualsMaxLen(t *testing.T) {
	// "café" has 4 runes, maxLen=4 → no truncation.
	assert.Equal(t, "café", Truncate("café", 4, "..."))
}

func TestTruncate_ShortString(t *testing.T) {
	// String shorter than maxLen in both bytes and runes.
	assert.Equal(t, "hi", Truncate("hi", 100, "..."))
}
