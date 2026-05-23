package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	GatewayEntryMatchExact  = "exact"
	GatewayEntryMatchPrefix = "prefix"
	GatewayEntryMatchRegex  = "regex"

	GatewayEntryUpstreamAnthropic   = "anthropic"
	GatewayEntryUpstreamOpenAI      = "openai"
	GatewayEntryUpstreamGemini      = "gemini"
	GatewayEntryUpstreamAntigravity = "antigravity"

	GatewayEntryStrategyPass    = "pass"
	GatewayEntryStrategyRewrite = "rewrite"
	GatewayEntryStrategyBlock   = "block"
)

type GatewayEntryRule struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	Enabled           bool    `json:"enabled"`
	MatchType         string  `json:"match_type"`
	Path              string  `json:"path"`
	UpstreamType      string  `json:"upstream_type"`
	InterceptStrategy string  `json:"intercept_strategy"`
	RewriteTarget     string  `json:"rewrite_target"`
	GroupIDs          []int64 `json:"group_ids"`
	Priority          int     `json:"priority"`
}

type GatewayEntryRuleMatch struct {
	Rule          GatewayEntryRule
	RewrittenPath string
}

type CustomEndpointView struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Description string `json:"description"`
}

func ParseGatewayEntryRules(raw string) ([]GatewayEntryRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []GatewayEntryRule{}, nil
	}
	var rules []GatewayEntryRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i] = NormalizeGatewayEntryRule(rules[i])
	}
	return rules, nil
}

func NormalizeGatewayEntryRule(rule GatewayEntryRule) GatewayEntryRule {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Description = strings.TrimSpace(rule.Description)
	rule.MatchType = strings.ToLower(strings.TrimSpace(rule.MatchType))
	if rule.MatchType == "" {
		rule.MatchType = GatewayEntryMatchPrefix
	}
	rule.Path = normalizeGatewayEntryPath(rule.Path)
	rule.UpstreamType = strings.ToLower(strings.TrimSpace(rule.UpstreamType))
	if rule.UpstreamType == "" {
		rule.UpstreamType = GatewayEntryUpstreamAnthropic
	}
	rule.InterceptStrategy = strings.ToLower(strings.TrimSpace(rule.InterceptStrategy))
	if rule.InterceptStrategy == "" {
		rule.InterceptStrategy = GatewayEntryStrategyRewrite
	}
	rule.RewriteTarget = normalizeGatewayEntryPath(rule.RewriteTarget)
	if rule.RewriteTarget == "" && rule.InterceptStrategy == GatewayEntryStrategyRewrite {
		rule.RewriteTarget = "/v1"
	}
	rule.GroupIDs = uniquePositiveInt64s(rule.GroupIDs)
	return rule
}

func ValidateGatewayEntryRules(rules []GatewayEntryRule) error {
	seenIDs := map[string]struct{}{}
	seenPaths := map[string]struct{}{}
	for i, rawRule := range rules {
		rule := NormalizeGatewayEntryRule(rawRule)
		if rule.ID == "" {
			return fmt.Errorf("gateway_entry_rules[%d].id is required", i)
		}
		if _, ok := seenIDs[rule.ID]; ok {
			return fmt.Errorf("gateway_entry_rules[%d].id %q is duplicated", i, rule.ID)
		}
		seenIDs[rule.ID] = struct{}{}
		if rule.Name == "" {
			return fmt.Errorf("gateway_entry_rules[%d].name is required", i)
		}
		if err := validateGatewayEntryPath(rule.Path); err != nil {
			return fmt.Errorf("gateway_entry_rules[%d].path: %w", i, err)
		}
		pathKey := rule.MatchType + ":" + rule.Path
		if _, ok := seenPaths[pathKey]; ok {
			return fmt.Errorf("gateway_entry_rules[%d].path %q is duplicated", i, rule.Path)
		}
		seenPaths[pathKey] = struct{}{}
		switch rule.MatchType {
		case GatewayEntryMatchExact, GatewayEntryMatchPrefix:
		case GatewayEntryMatchRegex:
			if _, err := regexp.Compile(rule.Path); err != nil {
				return fmt.Errorf("gateway_entry_rules[%d].path regex invalid: %w", i, err)
			}
		default:
			return fmt.Errorf("gateway_entry_rules[%d].match_type %q is invalid", i, rule.MatchType)
		}
		switch rule.UpstreamType {
		case GatewayEntryUpstreamAnthropic, GatewayEntryUpstreamOpenAI, GatewayEntryUpstreamGemini, GatewayEntryUpstreamAntigravity:
		default:
			return fmt.Errorf("gateway_entry_rules[%d].upstream_type %q is invalid", i, rule.UpstreamType)
		}
		switch rule.InterceptStrategy {
		case GatewayEntryStrategyPass, GatewayEntryStrategyRewrite, GatewayEntryStrategyBlock:
		default:
			return fmt.Errorf("gateway_entry_rules[%d].intercept_strategy %q is invalid", i, rule.InterceptStrategy)
		}
		if rule.InterceptStrategy == GatewayEntryStrategyRewrite {
			if err := validateGatewayRewriteTarget(rule.RewriteTarget); err != nil {
				return fmt.Errorf("gateway_entry_rules[%d].rewrite_target: %w", i, err)
			}
		}
	}
	return nil
}

func MatchGatewayEntryRule(rules []GatewayEntryRule, requestPath string) (GatewayEntryRuleMatch, bool) {
	requestPath = normalizeGatewayEntryPath(requestPath)
	if requestPath == "" {
		return GatewayEntryRuleMatch{}, false
	}
	candidates := make([]GatewayEntryRule, 0, len(rules))
	for _, rawRule := range rules {
		rule := NormalizeGatewayEntryRule(rawRule)
		if !rule.Enabled || rule.Path == "" {
			continue
		}
		if gatewayEntryRuleMatches(rule, requestPath) {
			candidates = append(candidates, rule)
		}
	}
	if len(candidates) == 0 {
		return GatewayEntryRuleMatch{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return len(candidates[i].Path) > len(candidates[j].Path)
	})
	rule := candidates[0]
	return GatewayEntryRuleMatch{
		Rule:          rule,
		RewrittenPath: rewriteGatewayEntryPath(rule, requestPath),
	}, true
}

func (m GatewayEntryRuleMatch) AllowsGroup(groupID int64) bool {
	ids := uniquePositiveInt64s(m.Rule.GroupIDs)
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if id == groupID {
			return true
		}
	}
	return false
}

func GatewayEntryRulesToCustomEndpoints(baseURL string, rules []GatewayEntryRule) []CustomEndpointView {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	out := make([]CustomEndpointView, 0, len(rules))
	for _, rawRule := range rules {
		rule := NormalizeGatewayEntryRule(rawRule)
		if !rule.Enabled || rule.Path == "" || rule.InterceptStrategy == GatewayEntryStrategyBlock {
			continue
		}
		endpoint := rule.Path
		if baseURL != "" {
			endpoint = baseURL + rule.Path
		}
		out = append(out, CustomEndpointView{
			Name:        rule.Name,
			Endpoint:    endpoint,
			Description: rule.Description,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func mergeCustomEndpointsWithGatewayEntries(customEndpointsRaw string, baseURL string, rules []GatewayEntryRule) string {
	var endpoints []CustomEndpointView
	raw := strings.TrimSpace(customEndpointsRaw)
	if raw != "" && raw != "[]" {
		_ = json.Unmarshal([]byte(raw), &endpoints)
	}
	endpoints = append(endpoints, GatewayEntryRulesToCustomEndpoints(baseURL, rules)...)
	if endpoints == nil {
		endpoints = []CustomEndpointView{}
	}
	data, err := json.Marshal(endpoints)
	if err != nil {
		return customEndpointsRaw
	}
	return string(data)
}

func gatewayEntryRuleMatches(rule GatewayEntryRule, requestPath string) bool {
	switch rule.MatchType {
	case GatewayEntryMatchExact:
		return requestPath == rule.Path
	case GatewayEntryMatchRegex:
		ok, err := regexp.MatchString(rule.Path, requestPath)
		return err == nil && ok
	default:
		return requestPath == rule.Path || strings.HasPrefix(requestPath, rule.Path+"/")
	}
}

func rewriteGatewayEntryPath(rule GatewayEntryRule, requestPath string) string {
	switch rule.InterceptStrategy {
	case GatewayEntryStrategyBlock:
		return ""
	case GatewayEntryStrategyPass:
		return requestPath
	}
	target := normalizeGatewayEntryPath(rule.RewriteTarget)
	if target == "" {
		target = "/v1"
	}
	if rule.MatchType != GatewayEntryMatchPrefix {
		return target
	}
	suffix := strings.TrimPrefix(requestPath, rule.Path)
	if suffix == "" {
		return target
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	if suffix == target || strings.HasPrefix(suffix, strings.TrimRight(target, "/")+"/") {
		return suffix
	}
	if target == "/" {
		return suffix
	}
	return strings.TrimRight(target, "/") + suffix
}

func normalizeGatewayEntryPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func validateGatewayEntryPath(path string) error {
	if path == "" || path == "/" {
		return fmt.Errorf("must be a non-root path")
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("must start with /")
	}
	lower := strings.ToLower(path)
	for _, reserved := range []string{"/api", "/admin", "/health", "/setup", "/assets"} {
		if lower == reserved || strings.HasPrefix(lower, reserved+"/") {
			return fmt.Errorf("must not use reserved prefix %s", reserved)
		}
	}
	return nil
}

func validateGatewayRewriteTarget(path string) error {
	if path == "" {
		return fmt.Errorf("is required when intercept_strategy is rewrite")
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("must start with /")
	}
	return nil
}

func uniquePositiveInt64s(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
