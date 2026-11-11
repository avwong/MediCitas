package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const actorContextKey = "auth.actor"

// Authenticate requires a valid bearer token. A missing or invalid token gets
// 401. A valid token that does not describe a usable MediCitas identity (no
// role, several roles, clinic role without clinic) gets 403.
func (v *Verifier) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			unauthorized(c)
			return
		}

		claims, err := v.Verify(c.Request.Context(), rawToken)
		if err != nil {
			unauthorized(c)
			return
		}

		actor, err := NewActor(claims, rawToken)
		if err != nil {
			forbidden(c)
			return
		}

		c.Set(actorContextKey, actor)
		c.Next()
	}
}

// RequirePermission answers 403 unless the actor's role grants permission.
func RequirePermission(permission Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := ActorFrom(c)
		if !ok {
			unauthorized(c)
			return
		}

		if !actor.Can(permission) {
			forbidden(c)
			return
		}

		c.Next()
	}
}

// RequireService answers 403 unless the caller is the service account of
// clientID. It protects the internal routes that services call each other on.
func RequireService(clientID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := ActorFrom(c)
		if !ok {
			unauthorized(c)
			return
		}

		if !actor.IsService(clientID) {
			forbidden(c)
			return
		}

		c.Next()
	}
}

// ActorFrom returns the actor set by Authenticate.
func ActorFrom(c *gin.Context) (Actor, bool) {
	value, exists := c.Get(actorContextKey)
	if !exists {
		return Actor{}, false
	}

	actor, ok := value.(Actor)
	return actor, ok
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	return parts[1], true
}

func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="medicitas"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
}

func forbidden(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
}
