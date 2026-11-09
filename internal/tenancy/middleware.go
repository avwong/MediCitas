package tenancy

import (
	"errors"
	"net/http"

	"github.com/avwong/MediCitas/internal/auth"
	"github.com/gin-gonic/gin"
)

func (r *Resolver) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claimsValue, exists := c.Get(auth.ClaimsContextKey)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}

		claims, ok := claimsValue.(*auth.Claims)
		if !ok || claims.Subject == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			return
		}

		tenantContext, err := r.Resolve(
			c.Request.Context(),
			claims.Subject,
		)
		if err != nil {
			switch {
			case errors.Is(err, ErrIdentityNotFound),
				errors.Is(err, ErrInactiveUser),
				errors.Is(err, ErrTenantUnavailable):
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "forbidden",
				})
			default:
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.Set(ContextKey, tenantContext)

		c.Next()
	}
}

func FromGinContext(c *gin.Context) (Context, bool) {
	value, exists := c.Get(ContextKey)
	if !exists {
		return Context{}, false
	}

	tenantContext, ok := value.(Context)
	return tenantContext, ok
}
