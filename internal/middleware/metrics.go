package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	appmetrics "github.com/mustafa-oezdemir/shipping-service/internal/metrics"
)

func Metrics(metrics *appmetrics.Metrics) gin.HandlerFunc {
	return func(context *gin.Context) {
		started := time.Now()
		metrics.HTTPRequestsInFlight.Inc()
		defer metrics.HTTPRequestsInFlight.Dec()
		context.Next()
		route := context.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(context.Writer.Status())
		metrics.HTTPRequestsTotal.WithLabelValues(context.Request.Method, route, status).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(context.Request.Method, route, status).Observe(time.Since(started).Seconds())
	}
}
