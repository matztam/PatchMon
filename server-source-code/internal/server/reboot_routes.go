package server

// fork: host reboot routes. router.go only calls these two functions.

import (
	"github.com/go-chi/chi/v5"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/handler"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// registerRebootAgentRoutes adds the agent callbacks (API key auth).
func registerRebootAgentRoutes(r chi.Router, h *handler.RebootHandler, redisResolver *hostctx.RedisResolver, cfgResolver *hostctx.ConfigResolver) {
	limit := middleware.RateLimit(redisResolver, cfgResolver, middleware.RateLimitAgent)
	r.With(limit).Post("/hosts/reboot/capability", h.AgentCapability)
	r.With(limit).Post("/hosts/reboot/requests/{requestId}/ack", h.AgentAck)
}

// registerRebootAdminRoutes adds the session-authenticated routes.
func registerRebootAdminRoutes(r chi.Router, h *handler.RebootHandler, permissionsStore *store.PermissionsStore) {
	r.With(middleware.RequirePermission("can_view_hosts", permissionsStore)).Get("/hosts/{hostId}/reboot", h.GetHostReboot)
	r.With(middleware.RequirePermission("can_manage_hosts", permissionsStore)).Post("/hosts/{hostId}/reboot", h.RequestReboot)
	r.With(middleware.RequirePermission("can_view_hosts", permissionsStore), hostctx.RequireModule("patching_policies")).Get("/patching/policies/{id}/reboot", h.GetPolicyReboot)
	r.With(middleware.RequirePermission("can_manage_patching", permissionsStore), hostctx.RequireModule("patching_policies")).Put("/patching/policies/{id}/reboot", h.PutPolicyReboot)
}
