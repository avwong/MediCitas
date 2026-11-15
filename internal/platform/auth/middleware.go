package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const actorContextKey = "auth.actor"

// Authenticate exige un bearer token válido. Si falta o es inválido responde
// 401. Si es válido pero no describe una identidad utilizable de MediCitas
// (sin rol, con varios roles, o con rol de clínica sin clínica) responde 403.
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

// RequirePermission responde 403 si el rol del actor no concede el permiso.
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

// RequireService responde 403 si quien llama no es la cuenta de servicio de
// clientID. Protege las rutas internas con las que los servicios se llaman
// entre sí.
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

// ActorFrom devuelve el actor que dejó Authenticate en el contexto.
func ActorFrom(c *gin.Context) (Actor, bool) {
	value, exists := c.Get(actorContextKey)
	if !exists {
		return Actor{}, false
	}

	actor, ok := value.(Actor)
	return actor, ok
}

// CurrentActor devuelve el actor de la petición y responde 401 si no hay
// ninguno. Los handlers lo usan para no repetir esa comprobación.
func CurrentActor(c *gin.Context) (Actor, bool) {
	actor, ok := ActorFrom(c)
	if !ok {
		unauthorized(c)
	}

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
