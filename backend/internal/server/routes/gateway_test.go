package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGatewayRoutesTestRouter() *gin.Engine {
	return newGatewayRoutesTestRouterForPlatform(service.PlatformOpenAI)
}

func newGatewayRoutesTestRouterForPlatform(platform string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(1)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				GroupID: &groupID,
				Group:   &service.Group{Platform: platform},
			})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		&config.Config{},
	)

	return router
}

func TestGatewayRoutesOpenAIResponsesCompactPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/responses/compact",
		"/responses/compact",
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI responses handler", path)
	}
}

func TestGatewayRoutesOpenAIImagesPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-image-2","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI images handler", path)
	}
}

func TestGatewayRoutesClaudePrefixAliasesAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouterForPlatform(service.PlatformAnthropic)

	for _, path := range []string{
		"/claude/messages",
		"/claude/messages/count_tokens",
		"/claude/responses",
		"/claude/responses/compact",
		"/claude/chat/completions",
		"/claude/v1/messages",
		"/claude/v1/messages/count_tokens",
		"/claude/v1/responses",
		"/claude/v1/responses/compact",
		"/claude/v1/chat/completions",
	} {
		body := `{"model":"claude-sonnet-4","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Claude alias handler", path)
	}
}

func TestDispatchGatewayEntryRule_RewritesPrefixAndForcesPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	groupID := int64(7)
	router.NoRoute(dispatchGatewayEntryRules(
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				GroupID: &groupID,
				Group:   &service.Group{ID: groupID, Platform: service.PlatformAnthropic},
				User:    &service.User{ID: 1, Status: service.StatusActive, Concurrency: 1},
			})
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 1, Concurrency: 1})
			c.Next()
		}),
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		gatewayEntryRuleProviderFunc(func(*gin.Context) ([]service.GatewayEntryRule, error) {
			return []service.GatewayEntryRule{{
				ID:                "team-openai",
				Name:              "Team OpenAI",
				Enabled:           true,
				MatchType:         service.GatewayEntryMatchPrefix,
				Path:              "/team-openai",
				UpstreamType:      service.GatewayEntryUpstreamOpenAI,
				InterceptStrategy: service.GatewayEntryStrategyRewrite,
				RewriteTarget:     "/v1",
				GroupIDs:          []int64{groupID},
			}}, nil
		}),
	))

	req := httptest.NewRequest(http.MethodPost, "/team-openai/responses", strings.NewReader(`{"model":"gpt-5"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusNotFound, w.Code)
}

func TestDefaultGatewayEntryRules_RewriteV1PrefixesWithoutDuplicatingV1(t *testing.T) {
	rules := defaultGatewayEntryRules()

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/openai/responses", want: "/v1/responses"},
		{path: "/openai/v1/responses", want: "/v1/responses"},
		{path: "/claude/messages", want: "/v1/messages"},
		{path: "/claude/v1/messages", want: "/v1/messages"},
	} {
		match, ok := service.MatchGatewayEntryRule(rules, tc.path)
		require.True(t, ok, "path=%s", tc.path)
		require.Equal(t, tc.want, match.RewrittenPath, "path=%s", tc.path)
	}
}

func TestGatewayEntryRules_ConfiguredRulesOverrideDefaults(t *testing.T) {
	rules := append([]service.GatewayEntryRule{{
		ID:                "custom-openai-v1",
		Name:              "Custom OpenAI v1",
		Enabled:           true,
		MatchType:         service.GatewayEntryMatchPrefix,
		Path:              "/openai/v1",
		UpstreamType:      service.GatewayEntryUpstreamOpenAI,
		InterceptStrategy: service.GatewayEntryStrategyRewrite,
		RewriteTarget:     "/v1beta",
		Priority:          10,
	}}, defaultGatewayEntryRules()...)

	match, ok := service.MatchGatewayEntryRule(rules, "/openai/v1/models")

	require.True(t, ok)
	require.Equal(t, "custom-openai-v1", match.Rule.ID)
	require.Equal(t, "/v1beta/models", match.RewrittenPath)
}
