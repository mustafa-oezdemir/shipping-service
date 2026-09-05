package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
)

const roleKey = "shipping_role"

func RequireServiceToken(token string) gin.HandlerFunc {
	return func(context *gin.Context) {
		value := strings.TrimPrefix(context.GetHeader("Authorization"), "Bearer ")
		if value == "" || subtle.ConstantTimeCompare([]byte(value), []byte(token)) != 1 {
			context.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "valid service credentials are required"})
			return
		}
		context.Next()
	}
}

func RequireRole(roles ...models.Role) gin.HandlerFunc {
	return func(context *gin.Context) {
		role := models.Role(context.GetHeader("X-Shipping-Role"))
		for _, allowed := range roles {
			if role == allowed {
				context.Set(roleKey, role)
				context.Next()
				return
			}
		}
		context.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "shipping role is not authorized"})
	}
}

func CurrentRole(context *gin.Context) models.Role {
	value, _ := context.Get(roleKey)
	role, _ := value.(models.Role)
	return role
}
