package expense

// auth.go — auth middleware. expense-svc doesn't have its own session
// store; it asks dock to introspect Bearer tokens via
// /internal/v1/auth/verify (cached 30s in the SDK). Same shape as
// internal/plugins/packtunnel/auth.go.
//
// Every workspace member has read+write — no admin gate, this is a
// household ledger.

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	ctxKeyUserID      = "user_id"
	ctxKeyUserRole    = "user_role"
	ctxKeyWorkspaceID = "workspace_id"
)

// requireAuthViaDock — Bearer + AuthVerify; sets user_id /
// user_role / workspace_id on the gin context. No role gate.
func (p *Plugin) requireAuthViaDock() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractAccessToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		res, err := p.Dock.AuthVerifyWS(token, strings.TrimSpace(c.GetHeader("X-Workspace-Id")))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		c.Set(ctxKeyUserID, res.UserID)
		c.Set(ctxKeyUserRole, res.Role)
		c.Set(ctxKeyWorkspaceID, res.WorkspaceID)

		// Closed-by-default tenant access gate (Sprint 2 / task #196).
		// Root workspace always passes via dock-side bypass; non-root
		// requires an explicit workspace_plugin_access row enabled by
		// the platform admin. Fail-closed on lookup error — the
		// gate's whole point is "deny unless proven allowed".
		access, err := p.Dock.WorkspacePluginAccess(res.WorkspaceID, p.Name)
		if err != nil || access == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "plugin access check failed"})
			return
		}
		if !access.Enabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "workspace not granted access to expense"})
			return
		}
		c.Next()
	}
}

// extractAccessToken: Bearer header → ?access_token= → cookie. Same
// fallback chain as dock so iOS / browser clients work the same.
func extractAccessToken(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("Authorization")); v != "" {
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			return strings.TrimSpace(v[7:])
		}
	}
	if v := strings.TrimSpace(c.Query("access_token")); v != "" {
		return v
	}
	if v, err := c.Cookie("access_token"); err == nil && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}
