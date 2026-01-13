package pail

import (
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/evergreen-ci/utility"
)

// LifecycleRule represents a simplified S3 bucket lifecycle rule with commonly needed fields extracted.
type LifecycleRule struct {
	// ID is the unique identifier for the rule
	ID string

	// Prefix is the object key prefix that the rule applies to (extracted from Filter or deprecated Prefix field)
	Prefix string

	// Status indicates whether the rule is currently active ("Enabled" or "Disabled")
	Status string

	// ExpirationDays is the number of days after creation when objects expire (nil if not set)
	ExpirationDays *int32

	// TransitionToIADays is the number of days after creation when objects transition to STANDARD_IA (nil if not set)
	TransitionToIADays *int32

	// TransitionToGlacierDays is the number of days after creation when objects transition to GLACIER (nil if not set)
	TransitionToGlacierDays *int32

	// Transitions contains all transition rules with full details (empty if none)
	Transitions []Transition
}

// Transition represents a storage class transition with timing information.
type Transition struct {
	// Days is the number of days after creation when the transition occurs (nil if Date is used instead)
	Days *int32

	// StorageClass is the target storage class (e.g., "STANDARD_IA", "GLACIER", "DEEP_ARCHIVE")
	StorageClass string
}

// convertLifecycleRules converts AWS SDK lifecycle rules to simplified pail types.
func convertLifecycleRules(output *s3.GetBucketLifecycleConfigurationOutput) []LifecycleRule {
	if output == nil || output.Rules == nil {
		return []LifecycleRule{}
	}

	rules := make([]LifecycleRule, 0, len(output.Rules))
	for _, awsRule := range output.Rules {
		rule := LifecycleRule{
			ID:     utility.FromStringPtr(awsRule.ID),
			Prefix: extractPrefix(awsRule),
			Status: string(awsRule.Status),
		}

		// Extract expiration days
		if awsRule.Expiration != nil && awsRule.Expiration.Days != nil {
			rule.ExpirationDays = awsRule.Expiration.Days
		}

		// Extract transitions and find specific storage classes
		if len(awsRule.Transitions) > 0 {
			rule.Transitions = make([]Transition, 0, len(awsRule.Transitions))
			for _, awsTransition := range awsRule.Transitions {
				t := Transition{
					Days:         awsTransition.Days,
					StorageClass: string(awsTransition.StorageClass),
				}
				rule.Transitions = append(rule.Transitions, t)

				// Extract convenience fields for common storage classes
				if awsTransition.Days != nil {
					switch awsTransition.StorageClass {
					case s3Types.TransitionStorageClassStandardIa:
						if rule.TransitionToIADays == nil {
							rule.TransitionToIADays = awsTransition.Days
						}
					case s3Types.TransitionStorageClassGlacier:
						if rule.TransitionToGlacierDays == nil {
							rule.TransitionToGlacierDays = awsTransition.Days
						}
					}
				}
			}
		}

		rules = append(rules, rule)
	}

	return rules
}

// extractPrefix extracts the prefix from a lifecycle rule, handling both the Filter field and deprecated Prefix field.
func extractPrefix(rule s3Types.LifecycleRule) string {
	// Try Filter.Prefix first (current approach)
	if rule.Filter != nil {
		if rule.Filter.Prefix != nil {
			return *rule.Filter.Prefix
		}
		// Handle complex filter with And operator
		if rule.Filter.And != nil && rule.Filter.And.Prefix != nil {
			return *rule.Filter.And.Prefix
		}
	}

	// Fall back to deprecated Prefix field
	if rule.Prefix != nil {
		return *rule.Prefix
	}

	return ""
}

// FindMatchingRule finds the most specific lifecycle rule that matches the given file key.
// It uses longest-prefix matching: for "a/b/c/file.txt", it tries "a/b/c/", "a/b/", "a/", and "".
// Only enabled rules are considered. Returns nil if no matching enabled rule is found.
func FindMatchingRule(rules []LifecycleRule, fileKey string) *LifecycleRule {
	if len(rules) == 0 {
		return nil
	}

	// Extract prefix hierarchy from file key
	prefixes := extractPrefixHierarchy(fileKey)

	// For each prefix (from longest to shortest), find matching enabled rule
	for _, prefix := range prefixes {
		for i := range rules {
			rule := &rules[i]
			if rule.Status == "Enabled" && rule.Prefix == prefix {
				return rule
			}
		}
	}

	return nil
}

// extractPrefixHierarchy extracts all possible prefixes from a file key in descending order of specificity.
// For "a/b/c/file.txt", returns ["a/b/c/", "a/b/", "a/", ""].
// For "file.txt", returns [""].
func extractPrefixHierarchy(fileKey string) []string {
	var prefixes []string

	// Find all slash positions
	for i := len(fileKey) - 1; i >= 0; i-- {
		if fileKey[i] == '/' {
			// Add prefix including the slash
			prefixes = append(prefixes, fileKey[:i+1])
		}
	}

	// Add empty prefix as fallback (matches rules with no prefix filter)
	prefixes = append(prefixes, "")

	return prefixes
}
