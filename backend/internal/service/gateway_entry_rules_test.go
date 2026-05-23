//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGatewayEntryRules_NormalizesAndFiltersRules(t *testing.T) {
	raw := `[
		{
			"id":" openai-main ",
			"name":" OpenAI ",
			"enabled":true,
			"match_type":"prefix",
			"path":"/openai/",
			"upstream_type":"openai",
			"intercept_strategy":"rewrite",
			"rewrite_target":"/v1",
			"group_ids":[2,2,3],
			"priority":10
		},
		{"id":"disabled","name":"Disabled","enabled":false,"path":"/disabled"}
	]`

	rules, err := ParseGatewayEntryRules(raw)

	require.NoError(t, err)
	require.Len(t, rules, 2)
	require.Equal(t, "openai-main", rules[0].ID)
	require.Equal(t, "/openai", rules[0].Path)
	require.Equal(t, "/v1", rules[0].RewriteTarget)
	require.Equal(t, []int64{2, 3}, rules[0].GroupIDs)
	require.Equal(t, GatewayEntryMatchPrefix, rules[0].MatchType)
	require.Equal(t, GatewayEntryUpstreamOpenAI, rules[0].UpstreamType)
	require.Equal(t, GatewayEntryStrategyRewrite, rules[0].InterceptStrategy)
}

func TestValidateGatewayEntryRules_RejectsInvalidPath(t *testing.T) {
	rules := []GatewayEntryRule{{
		ID:                "bad",
		Name:              "Bad",
		Enabled:           true,
		MatchType:         GatewayEntryMatchPrefix,
		Path:              "/api/v1/bad",
		UpstreamType:      GatewayEntryUpstreamOpenAI,
		InterceptStrategy: GatewayEntryStrategyRewrite,
		RewriteTarget:     "/v1",
		GroupIDs:          []int64{1},
	}}

	err := ValidateGatewayEntryRules(rules)

	require.Error(t, err)
	require.Contains(t, err.Error(), "/api")
}

func TestMatchGatewayEntryRule_UsesPriorityAndSpecificity(t *testing.T) {
	rules := []GatewayEntryRule{
		{
			ID:                "low",
			Name:              "Low",
			Enabled:           true,
			MatchType:         GatewayEntryMatchPrefix,
			Path:              "/openai",
			UpstreamType:      GatewayEntryUpstreamOpenAI,
			InterceptStrategy: GatewayEntryStrategyRewrite,
			RewriteTarget:     "/v1",
			GroupIDs:          []int64{1},
			Priority:          1,
		},
		{
			ID:                "high",
			Name:              "High",
			Enabled:           true,
			MatchType:         GatewayEntryMatchPrefix,
			Path:              "/openai/v1",
			UpstreamType:      GatewayEntryUpstreamOpenAI,
			InterceptStrategy: GatewayEntryStrategyRewrite,
			RewriteTarget:     "/v1",
			GroupIDs:          []int64{1},
			Priority:          10,
		},
	}

	match, ok := MatchGatewayEntryRule(rules, "/openai/v1/responses")

	require.True(t, ok)
	require.Equal(t, "high", match.Rule.ID)
	require.Equal(t, "/v1/responses", match.RewrittenPath)
}

func TestMatchGatewayEntryRule_RequiresBoundGroup(t *testing.T) {
	rules := []GatewayEntryRule{{
		ID:                "openai",
		Name:              "OpenAI",
		Enabled:           true,
		MatchType:         GatewayEntryMatchPrefix,
		Path:              "/openai",
		UpstreamType:      GatewayEntryUpstreamOpenAI,
		InterceptStrategy: GatewayEntryStrategyRewrite,
		RewriteTarget:     "/v1",
		GroupIDs:          []int64{2},
	}}

	match, ok := MatchGatewayEntryRule(rules, "/openai/responses")

	require.True(t, ok)
	require.False(t, match.AllowsGroup(1))
	require.True(t, match.AllowsGroup(2))
}

func TestMatchGatewayEntryRule_DoesNotDuplicateRewriteTargetPrefix(t *testing.T) {
	rules := []GatewayEntryRule{{
		ID:                "openai",
		Name:              "OpenAI",
		Enabled:           true,
		MatchType:         GatewayEntryMatchPrefix,
		Path:              "/openai",
		UpstreamType:      GatewayEntryUpstreamOpenAI,
		InterceptStrategy: GatewayEntryStrategyRewrite,
		RewriteTarget:     "/v1",
		GroupIDs:          []int64{2},
	}}

	match, ok := MatchGatewayEntryRule(rules, "/openai/v1/models")

	require.True(t, ok)
	require.Equal(t, "/v1/models", match.RewrittenPath)
}
