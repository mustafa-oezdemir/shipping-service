package middleware

import "github.com/gin-gonic/gin"

func SecurityHeaders(enableHSTS bool) gin.HandlerFunc {
	return func(context *gin.Context) {
		headers := context.Writer.Header()
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		headers.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(self)")
		headers.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; media-src 'self' blob:; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		if enableHSTS {
			headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		context.Next()
	}
}
