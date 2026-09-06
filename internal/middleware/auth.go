package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/httpapi"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
)

const (
	roleKey          = "shipping_role"
	requestIDKey     = "request_id"
	sourceServiceKey = "source_service"
)

func RequireServiceToken(tokens ...string) gin.HandlerFunc {
	validTokens := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if trimmed := strings.TrimSpace(token); trimmed != "" {
			validTokens = append(validTokens, trimmed)
		}
	}
	return func(context *gin.Context) {
		candidate := strings.TrimSpace(strings.TrimPrefix(context.GetHeader("Authorization"), "Bearer "))
		for _, token := range validTokens {
			if candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
				context.Next()
				return
			}
		}
		httpapi.Error(context, http.StatusUnauthorized, "UNAUTHORIZED", "Valid service credentials are required", CurrentRequestID(context))
		context.Abort()
	}
}

func RequestMetadata(defaultSourceService string) gin.HandlerFunc {
	return func(context *gin.Context) {
		requestID := strings.TrimSpace(context.GetHeader("X-Request-ID"))
		if !validMetadataValue(requestID, 80) {
			requestID = generateRequestID()
		}
		sourceService := parseSourceService(context, defaultSourceService)
		context.Set(requestIDKey, requestID)
		context.Set(sourceServiceKey, sourceService)
		context.Header("X-Request-ID", requestID)
		context.Next()
	}
}

func RequireIdempotencyKey() gin.HandlerFunc {
	return func(context *gin.Context) {
		key := strings.TrimSpace(context.GetHeader("Idempotency-Key"))
		if !validMetadataValue(key, 128) {
			httpapi.Error(context, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must contain 1 to 128 safe characters", CurrentRequestID(context))
			context.Abort()
			return
		}
		context.Request.Header.Set("Idempotency-Key", key)
		context.Next()
	}
}

func NoStore() gin.HandlerFunc {
	return func(context *gin.Context) {
		context.Header("Cache-Control", "no-store")
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
		httpapi.Error(context, http.StatusForbidden, "FORBIDDEN", "Shipping role is not authorized", CurrentRequestID(context))
		context.Abort()
	}
}

func CurrentRole(context *gin.Context) models.Role {
	value, _ := context.Get(roleKey)
	role, _ := value.(models.Role)
	return role
}

func CurrentRequestID(context *gin.Context) string {
	value, _ := context.Get(requestIDKey)
	requestID, _ := value.(string)
	return requestID
}

func SourceService(context *gin.Context, fallback string) string {
	value, _ := context.Get(sourceServiceKey)
	sourceService, _ := value.(string)
	if strings.TrimSpace(sourceService) == "" {
		return fallback
	}
	return sourceService
}

func parseSourceService(context *gin.Context, fallback string) string {
	if value := strings.TrimSpace(context.GetHeader("X-Service-Name")); value != "" {
		if validMetadataValue(value, 80) {
			return value
		}
		return fallback
	}
	if value := strings.TrimSpace(context.GetHeader("User-Agent")); value != "" {
		if slash := strings.Index(value, "/"); slash > 0 {
			value = value[:slash]
		}
		if validMetadataValue(value, 80) {
			return value
		}
	}
	return fallback
}

func validMetadataValue(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' || character == ':' || character == '/' {
			continue
		}
		return false
	}
	return true
}

func generateRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "req_fallback"
	}
	return "req_" + hex.EncodeToString(buffer)
}
