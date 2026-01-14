package pail

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPrefixHierarchy(t *testing.T) {
	tests := []struct {
		name     string
		fileKey  string
		expected []string
	}{
		{
			name:     "nested path",
			fileKey:  "a/b/c/file.txt",
			expected: []string{"a/b/c/", "a/b/", "a/", ""},
		},
		{
			name:     "single level",
			fileKey:  "sandbox/file.txt",
			expected: []string{"sandbox/", ""},
		},
		{
			name:     "no slash",
			fileKey:  "file.txt",
			expected: []string{""},
		},
		{
			name:     "empty key",
			fileKey:  "",
			expected: []string{""},
		},
		{
			name:     "trailing slash",
			fileKey:  "a/b/c/",
			expected: []string{"a/b/c/", "a/b/", "a/", ""},
		},
		{
			name:     "deep nesting",
			fileKey:  "project/env/logs/2024/01/file.log",
			expected: []string{"project/env/logs/2024/01/", "project/env/logs/2024/", "project/env/logs/", "project/env/", "project/", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractPrefixHierarchy(tt.fileKey)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindMatchingRule(t *testing.T) {
	int32Ptr := func(v int32) *int32 { return &v }

	rules := []LifecycleRule{
		{ID: "r1", Prefix: "sandbox/", Status: "Enabled", ExpirationDays: int32Ptr(30)},
		{ID: "r2", Prefix: "sandbox/temp/", Status: "Enabled", ExpirationDays: int32Ptr(7)},
		{ID: "r3", Prefix: "", Status: "Enabled", ExpirationDays: int32Ptr(90)},
		{ID: "r4", Prefix: "disabled/", Status: "Disabled", ExpirationDays: int32Ptr(1)},
	}

	// Longest prefix match
	result := FindMatchingRule(rules, "sandbox/temp/file.txt")
	assert.Equal(t, "r2", result.ID)
	require.NotNil(t, result.ExpirationDays)
	assert.Equal(t, int32(7), *result.ExpirationDays)

	// Parent prefix match
	result = FindMatchingRule(rules, "sandbox/other/file.txt")
	assert.Equal(t, "r1", result.ID)

	// Default rule match
	result = FindMatchingRule(rules, "other/file.txt")
	assert.Equal(t, "r3", result.ID)

	// Disabled rule skipped
	result = FindMatchingRule(rules, "disabled/file.txt")
	assert.Equal(t, "r3", result.ID)

	// No match
	result = FindMatchingRule([]LifecycleRule{{ID: "r1", Prefix: "sandbox/", Status: "Enabled"}}, "prod/file.txt")
	assert.Nil(t, result)

	// Empty rules
	assert.Nil(t, FindMatchingRule([]LifecycleRule{}, "file.txt"))
}
