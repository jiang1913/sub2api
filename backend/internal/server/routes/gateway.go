package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterGatewayRoutes registers gateway routes for Claude/OpenAI/Gemini compatibility.
func RegisterGatewayRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	bodyLimit := middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	clientRequestID := middleware.ClientRequestID()
	opsErrorLogger := handler.OpsErrorLoggerMiddleware(opsService)
	endpointNorm := handler.InboundEndpointMiddleware()

	requireGroupAnthropic := middleware.RequireGroupAssignment(settingService, middleware.AnthropicErrorWriter)
	requireGroupGoogle := middleware.RequireGroupAssignment(settingService, middleware.GoogleErrorWriter)

	registerClaudeCompatibleRoutes(r.Group("/v1"), h, bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, apiKeyAuth, requireGroupAnthropic)

	gemini := r.Group("/v1beta")
	gemini.Use(bodyLimit)
	gemini.Use(clientRequestID)
	gemini.Use(opsErrorLogger)
	gemini.Use(endpointNorm)
	gemini.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	gemini.Use(requireGroupGoogle)
	{
		gemini.GET("/models", h.Gateway.GeminiV1BetaListModels)
		gemini.GET("/models/:model", h.Gateway.GeminiV1BetaGetModel)
		gemini.POST("/models/*modelAction", h.Gateway.GeminiV1BetaModels)
	}

	responsesHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformOpenAI {
			h.OpenAIGateway.Responses(c)
			return
		}
		h.Gateway.Responses(c)
	}
	r.POST("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, responsesHandler)
	r.POST("/responses/*subpath", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, responsesHandler)
	r.GET("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.OpenAIGateway.ResponsesWebSocket)

	codexDirect := r.Group("/backend-api/codex")
	codexDirect.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic)
	{
		codexDirect.POST("/responses", responsesHandler)
		codexDirect.POST("/responses/*subpath", responsesHandler)
		codexDirect.GET("/responses", h.OpenAIGateway.ResponsesWebSocket)
	}

	r.POST("/chat/completions", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformOpenAI {
			h.OpenAIGateway.ChatCompletions(c)
			return
		}
		h.Gateway.ChatCompletions(c)
	})
	r.POST("/images/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, openAIImagesHandler(h))
	r.POST("/images/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, openAIImagesHandler(h))

	r.GET("/antigravity/models", gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.Gateway.AntigravityModels)

	antigravityV1 := r.Group("/antigravity/v1")
	antigravityV1.Use(bodyLimit)
	antigravityV1.Use(clientRequestID)
	antigravityV1.Use(opsErrorLogger)
	antigravityV1.Use(endpointNorm)
	antigravityV1.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1.Use(gin.HandlerFunc(apiKeyAuth))
	antigravityV1.Use(requireGroupAnthropic)
	{
		antigravityV1.POST("/messages", h.Gateway.Messages)
		antigravityV1.POST("/messages/count_tokens", h.Gateway.CountTokens)
		antigravityV1.GET("/models", h.Gateway.AntigravityModels)
		antigravityV1.GET("/usage", h.Gateway.Usage)
	}

	antigravityV1Beta := r.Group("/antigravity/v1beta")
	antigravityV1Beta.Use(bodyLimit)
	antigravityV1Beta.Use(clientRequestID)
	antigravityV1Beta.Use(opsErrorLogger)
	antigravityV1Beta.Use(endpointNorm)
	antigravityV1Beta.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1Beta.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	antigravityV1Beta.Use(requireGroupGoogle)
	{
		antigravityV1Beta.GET("/models", h.Gateway.GeminiV1BetaListModels)
		antigravityV1Beta.GET("/models/:model", h.Gateway.GeminiV1BetaGetModel)
		antigravityV1Beta.POST("/models/*modelAction", h.Gateway.GeminiV1BetaModels)
	}

	r.NoRoute(dispatchGatewayEntryRules(
		h,
		apiKeyAuth,
		bodyLimit,
		clientRequestID,
		opsErrorLogger,
		endpointNorm,
		requireGroupAnthropic,
		requireGroupGoogle,
		gatewayEntryRulesFromSettingService{settingService: settingService},
	))
}

type gatewayEntryRuleProvider interface {
	GatewayEntryRules(*gin.Context) ([]service.GatewayEntryRule, error)
}

type gatewayEntryRuleProviderFunc func(*gin.Context) ([]service.GatewayEntryRule, error)

func (fn gatewayEntryRuleProviderFunc) GatewayEntryRules(c *gin.Context) ([]service.GatewayEntryRule, error) {
	return fn(c)
}

type gatewayEntryRulesFromSettingService struct {
	settingService *service.SettingService
}

func (p gatewayEntryRulesFromSettingService) GatewayEntryRules(c *gin.Context) ([]service.GatewayEntryRule, error) {
	rules := defaultGatewayEntryRules()
	if p.settingService == nil {
		return rules, nil
	}
	configured, err := p.settingService.GetGatewayEntryRules(c.Request.Context())
	if err != nil {
		return nil, err
	}
	return append(configured, rules...), nil
}

func defaultGatewayEntryRules() []service.GatewayEntryRule {
	return []service.GatewayEntryRule{
		{
			ID:                "default-openai-v1",
			Name:              "OpenAI v1",
			Enabled:           true,
			MatchType:         service.GatewayEntryMatchPrefix,
			Path:              "/openai/v1",
			UpstreamType:      service.GatewayEntryUpstreamOpenAI,
			InterceptStrategy: service.GatewayEntryStrategyRewrite,
			RewriteTarget:     "/v1",
			Priority:          -900,
		},
		{
			ID:                "default-openai",
			Name:              "OpenAI",
			Enabled:           true,
			MatchType:         service.GatewayEntryMatchPrefix,
			Path:              "/openai",
			UpstreamType:      service.GatewayEntryUpstreamOpenAI,
			InterceptStrategy: service.GatewayEntryStrategyRewrite,
			RewriteTarget:     "/v1",
			Priority:          -1000,
		},
		{
			ID:                "default-claude-v1",
			Name:              "Claude v1",
			Enabled:           true,
			MatchType:         service.GatewayEntryMatchPrefix,
			Path:              "/claude/v1",
			UpstreamType:      service.GatewayEntryUpstreamAnthropic,
			InterceptStrategy: service.GatewayEntryStrategyRewrite,
			RewriteTarget:     "/v1",
			Priority:          -900,
		},
		{
			ID:                "default-claude",
			Name:              "Claude",
			Enabled:           true,
			MatchType:         service.GatewayEntryMatchPrefix,
			Path:              "/claude",
			UpstreamType:      service.GatewayEntryUpstreamAnthropic,
			InterceptStrategy: service.GatewayEntryStrategyRewrite,
			RewriteTarget:     "/v1",
			Priority:          -1000,
		},
	}
}

func dispatchGatewayEntryRules(
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	bodyLimit gin.HandlerFunc,
	clientRequestID gin.HandlerFunc,
	opsErrorLogger gin.HandlerFunc,
	endpointNorm gin.HandlerFunc,
	requireGroupAnthropic gin.HandlerFunc,
	requireGroupGoogle gin.HandlerFunc,
	provider gatewayEntryRuleProvider,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h == nil || provider == nil || c.Request == nil || c.Request.URL == nil {
			c.Status(http.StatusNotFound)
			return
		}
		rules, err := provider.GatewayEntryRules(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load gateway entry rules"}})
			return
		}
		match, ok := service.MatchGatewayEntryRule(rules, c.Request.URL.Path)
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		if match.Rule.InterceptStrategy == service.GatewayEntryStrategyBlock {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "gateway entry rule blocked this request"}})
			return
		}
		if match.RewrittenPath == "" {
			c.Status(http.StatusNotFound)
			return
		}

		c.Request.URL.Path = match.RewrittenPath
		c.Request.RequestURI = match.RewrittenPath
		setGatewayEntryForcePlatform(c, match.Rule.UpstreamType)

		runGatewayEntryMiddleware(c, bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth))
		if c.IsAborted() || c.Writer.Written() {
			return
		}

		apiKey, _ := middleware.GetAPIKeyFromContext(c)
		groupID := int64(0)
		if apiKey != nil && apiKey.GroupID != nil {
			groupID = *apiKey.GroupID
		}
		if !match.AllowsGroup(groupID) {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "API key group is not allowed to use this gateway entry"}})
			return
		}

		if match.Rule.UpstreamType == service.GatewayEntryUpstreamGemini {
			runGatewayEntryMiddleware(c, requireGroupGoogle)
		} else {
			runGatewayEntryMiddleware(c, requireGroupAnthropic)
		}
		if c.IsAborted() || c.Writer.Written() {
			return
		}

		dispatchRewrittenGatewayRequest(c, h, match.Rule.UpstreamType, match.RewrittenPath)
	}
}

func runGatewayEntryMiddleware(c *gin.Context, handlers ...gin.HandlerFunc) {
	for _, fn := range handlers {
		if fn == nil {
			continue
		}
		fn(c)
		if c.IsAborted() || c.Writer.Written() {
			return
		}
	}
}

func setGatewayEntryForcePlatform(c *gin.Context, upstreamType string) {
	platform := gatewayEntryUpstreamPlatform(upstreamType)
	if platform == "" {
		return
	}
	ctx := context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, platform)
	c.Request = c.Request.WithContext(ctx)
	c.Set(string(middleware.ContextKeyForcePlatform), platform)
}

func gatewayEntryUpstreamPlatform(upstreamType string) string {
	switch upstreamType {
	case service.GatewayEntryUpstreamOpenAI:
		return service.PlatformOpenAI
	case service.GatewayEntryUpstreamGemini:
		return service.PlatformGemini
	case service.GatewayEntryUpstreamAntigravity:
		return service.PlatformAntigravity
	default:
		return service.PlatformAnthropic
	}
}

func dispatchRewrittenGatewayRequest(c *gin.Context, h *handler.Handlers, upstreamType, path string) {
	switch {
	case c.Request.Method == http.MethodPost && path == "/v1/messages":
		if upstreamType == service.GatewayEntryUpstreamOpenAI {
			h.OpenAIGateway.Messages(c)
			return
		}
		h.Gateway.Messages(c)
	case c.Request.Method == http.MethodPost && path == "/v1/messages/count_tokens":
		countTokensHandler(h)(c)
	case c.Request.Method == http.MethodGet && path == "/v1/models":
		h.Gateway.Models(c)
	case c.Request.Method == http.MethodGet && path == "/v1/usage":
		h.Gateway.Usage(c)
	case c.Request.Method == http.MethodPost && (path == "/v1/responses" || strings.HasPrefix(path, "/v1/responses/")):
		if upstreamType == service.GatewayEntryUpstreamOpenAI {
			h.OpenAIGateway.Responses(c)
			return
		}
		h.Gateway.Responses(c)
	case c.Request.Method == http.MethodGet && path == "/v1/responses":
		h.OpenAIGateway.ResponsesWebSocket(c)
	case c.Request.Method == http.MethodPost && path == "/v1/chat/completions":
		if upstreamType == service.GatewayEntryUpstreamOpenAI {
			h.OpenAIGateway.ChatCompletions(c)
			return
		}
		h.Gateway.ChatCompletions(c)
	case c.Request.Method == http.MethodPost && (path == "/v1/images/generations" || path == "/v1/images/edits"):
		openAIImagesHandler(h)(c)
	case c.Request.Method == http.MethodGet && path == "/v1beta/models":
		h.Gateway.GeminiV1BetaListModels(c)
	case c.Request.Method == http.MethodGet && strings.HasPrefix(path, "/v1beta/models/"):
		c.Params = append(c.Params, gin.Param{Key: "model", Value: strings.TrimPrefix(path, "/v1beta/models/")})
		h.Gateway.GeminiV1BetaGetModel(c)
	case c.Request.Method == http.MethodPost && strings.HasPrefix(path, "/v1beta/models/"):
		c.Params = append(c.Params, gin.Param{Key: "modelAction", Value: strings.TrimPrefix(path, "/v1beta/models")})
		h.Gateway.GeminiV1BetaModels(c)
	default:
		c.Status(http.StatusNotFound)
	}
}

func registerClaudeCompatibleRoutes(
	group *gin.RouterGroup,
	h *handler.Handlers,
	bodyLimit gin.HandlerFunc,
	clientRequestID gin.HandlerFunc,
	opsErrorLogger gin.HandlerFunc,
	endpointNorm gin.HandlerFunc,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	requireGroupAnthropic gin.HandlerFunc,
) {
	group.Use(bodyLimit)
	group.Use(clientRequestID)
	group.Use(opsErrorLogger)
	group.Use(endpointNorm)
	group.Use(gin.HandlerFunc(apiKeyAuth))
	group.Use(requireGroupAnthropic)
	{
		group.POST("/messages", func(c *gin.Context) {
			if getGroupPlatform(c) == service.PlatformOpenAI {
				h.OpenAIGateway.Messages(c)
				return
			}
			h.Gateway.Messages(c)
		})
		group.POST("/messages/count_tokens", countTokensHandler(h))
		group.GET("/models", h.Gateway.Models)
		group.GET("/usage", h.Gateway.Usage)
		group.POST("/responses", func(c *gin.Context) {
			if getGroupPlatform(c) == service.PlatformOpenAI {
				h.OpenAIGateway.Responses(c)
				return
			}
			h.Gateway.Responses(c)
		})
		group.POST("/responses/*subpath", func(c *gin.Context) {
			if getGroupPlatform(c) == service.PlatformOpenAI {
				h.OpenAIGateway.Responses(c)
				return
			}
			h.Gateway.Responses(c)
		})
		group.GET("/responses", h.OpenAIGateway.ResponsesWebSocket)
		group.POST("/chat/completions", func(c *gin.Context) {
			if getGroupPlatform(c) == service.PlatformOpenAI {
				h.OpenAIGateway.ChatCompletions(c)
				return
			}
			h.Gateway.ChatCompletions(c)
		})
		group.POST("/images/generations", openAIImagesHandler(h))
		group.POST("/images/edits", openAIImagesHandler(h))
	}
}

func countTokensHandler(h *handler.Handlers) gin.HandlerFunc {
	return func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformOpenAI {
			c.JSON(http.StatusNotFound, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Token counting is not supported for this platform",
				},
			})
			return
		}
		h.Gateway.CountTokens(c)
	}
}

func openAIImagesHandler(h *handler.Handlers) gin.HandlerFunc {
	return func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformOpenAI {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Images API is not supported for this platform",
				},
			})
			return
		}
		h.OpenAIGateway.Images(c)
	}
}

// getGroupPlatform extracts the group platform from the API Key stored in context.
func getGroupPlatform(c *gin.Context) string {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}
