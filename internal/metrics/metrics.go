package metrics

import (
	"context"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
)

var (
	OutboxPending    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "shipping_outbox_pending_total", Help: "Current number of pending Shipping outbox events."})
	OutboxFailed     = prometheus.NewGauge(prometheus.GaugeOpts{Name: "shipping_outbox_failed_total", Help: "Current number of failed Shipping outbox events."})
	Callbacks        = prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_callback_total", Help: "Shipping callback delivery attempts."})
	CallbackFailures = prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_callback_failures_total", Help: "Failed Shipping callback delivery attempts."})
)

func init() {
	prometheus.MustRegister(OutboxPending, OutboxFailed, Callbacks, CallbackFailures)
}

func RefreshOutbox(ctx context.Context, database *gorm.DB) {
	if database == nil {
		return
	}
	var pending, failed int64
	if database.WithContext(ctx).Model(&models.OutboxEvent{}).Where("status = ?", models.OutboxStatusPending).Count(&pending).Error == nil {
		OutboxPending.Set(float64(pending))
	}
	if database.WithContext(ctx).Model(&models.OutboxEvent{}).Where("status = ?", models.OutboxStatusFailed).Count(&failed).Error == nil {
		OutboxFailed.Set(float64(failed))
	}
}
