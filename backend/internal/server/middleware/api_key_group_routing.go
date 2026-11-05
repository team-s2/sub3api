package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// routeAPIKeyByRequestModel binds a multi-group key to the group serving the requested
// model. It must run before group checks and subscription loading, which then apply to
// the selected group. It returns false after aborting the request.
func routeAPIKeyByRequestModel(c *gin.Context, apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, apiKey *service.APIKey) (*service.APIKey, bool) {
	if !apiKey.HasExtraGroups() {
		return apiKey, true
	}
	model, ok := requestModelForGroupRouting(c)
	if !ok {
		return nil, false
	}
	ctx := c.Request.Context()
	usable := func(group *service.Group) bool {
		if !group.IsSubscriptionType() {
			return apiKey.User.CanBindGroup(group.ID, group.IsExclusive)
		}
		if subscriptionService == nil {
			return true
		}
		_, err := subscriptionService.GetActiveSubscription(ctx, apiKey.User.ID, group.ID)
		return err == nil
	}
	endpoint := service.CompositeRouteEndpointForPath(c.Request.URL.Path)
	routed := apiKeyService.RouteAPIKeyByModel(ctx, apiKey, model, endpoint, usable)
	SetOpsFallbackAPIKey(c, routed)
	return routed, true
}

func requestModelForGroupRouting(c *gin.Context) (string, bool) {
	if model := groupModelAllowlistModelFromParams(c); model != "" {
		return model, true
	}
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		body, ok := prereadRequestBody(c)
		if !ok {
			return "", false
		}
		if model := requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body); model != "" {
			return model, true
		}
	}
	return strings.TrimSpace(c.Query("model")), true
}
