package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
)

type Metrics struct {
	HTTPRequestsTotal         *prometheus.CounterVec
	HTTPRequestDuration       *prometheus.HistogramVec
	HTTPRequestsInFlight      prometheus.Gauge
	HealthLive                prometheus.Gauge
	HealthReady               prometheus.Gauge
	ShipmentsCreated          *prometheus.CounterVec
	ShipmentStatusTransitions *prometheus.CounterVec
	ShipmentOperations        *prometheus.CounterVec
	DeliveryFailures          prometheus.Counter
	DeliveryDuration          prometheus.Histogram
	TimeToFirstReceipt        prometheus.Histogram
	ReturnsCreated            prometheus.Counter
	ReturnStatusTransitions   *prometheus.CounterVec
	CallbackRequests          *prometheus.CounterVec
	CallbackDuration          prometheus.Histogram
	OutboxDelivered           prometheus.Counter
	OutboxRetries             prometheus.Counter
	APIAuthFailures           prometheus.Counter
	EmployeeLogins            *prometheus.CounterVec
	QRScans                   *prometheus.CounterVec
	EcommerceDependencyUp     prometheus.Gauge
}

var (
	defaultMu      sync.RWMutex
	defaultMetrics *Metrics
)

func New(registerer prometheus.Registerer) *Metrics {
	m := &Metrics{
		HTTPRequestsTotal:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_http_requests_total", Help: "Total Shipping HTTP requests."}, []string{"method", "route", "status"}),
		HTTPRequestDuration:       prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "shipping_http_request_duration_seconds", Help: "Shipping HTTP request duration in seconds."}, []string{"method", "route", "status"}),
		HTTPRequestsInFlight:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "shipping_http_requests_in_flight", Help: "Shipping HTTP requests currently in flight."}),
		HealthLive:                prometheus.NewGauge(prometheus.GaugeOpts{Name: "shipping_health_live", Help: "Whether the Shipping process is live."}),
		HealthReady:               prometheus.NewGauge(prometheus.GaugeOpts{Name: "shipping_health_ready", Help: "Whether Shipping is ready to serve requests."}),
		ShipmentsCreated:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_shipments_created_total", Help: "Shipping shipments created."}, []string{"shipment_type"}),
		ShipmentStatusTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_shipment_status_transitions_total", Help: "Accepted shipment status transitions."}, []string{"shipment_type", "status"}),
		ShipmentOperations:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_shipment_operations_total", Help: "Aggregate shipment operations."}, []string{"operation"}),
		DeliveryFailures:          prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_delivery_failures_total", Help: "Shipments transitioned to delivery_failed."}),
		DeliveryDuration:          prometheus.NewHistogram(prometheus.HistogramOpts{Name: "shipping_delivery_duration_seconds", Help: "Time from shipment creation to delivery.", Buckets: prometheus.ExponentialBuckets(300, 2, 12)}),
		TimeToFirstReceipt:        prometheus.NewHistogram(prometheus.HistogramOpts{Name: "shipping_time_to_first_receipt_seconds", Help: "Time from shipment creation to receipt by Shipping.", Buckets: prometheus.ExponentialBuckets(60, 2, 12)}),
		ReturnsCreated:            prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_returns_created_total", Help: "Customer returns created."}),
		ReturnStatusTransitions:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_return_status_transitions_total", Help: "Accepted return status transitions."}, []string{"status"}),
		CallbackRequests:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_ecommerce_callback_requests_total", Help: "Shipping to E-Commerce callback attempts."}, []string{"result"}),
		CallbackDuration:          prometheus.NewHistogram(prometheus.HistogramOpts{Name: "shipping_ecommerce_callback_duration_seconds", Help: "Shipping to E-Commerce callback duration."}),
		OutboxDelivered:           prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_outbox_delivered_total", Help: "Outbox events delivered successfully."}),
		OutboxRetries:             prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_outbox_retry_total", Help: "Outbox events scheduled for retry."}),
		APIAuthFailures:           prometheus.NewCounter(prometheus.CounterOpts{Name: "shipping_api_auth_failures_total", Help: "Rejected service API authentication attempts."}),
		EmployeeLogins:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_employee_login_total", Help: "Shipping portal login attempts."}, []string{"result"}),
		QRScans:                   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shipping_qr_scans_total", Help: "Aggregate QR scan attempts."}, []string{"result"}),
		EcommerceDependencyUp:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "shipping_ecommerce_dependency_up", Help: "Whether the most recent E-Commerce callback succeeded."}),
	}
	registerer.MustRegister(
		m.HTTPRequestsTotal, m.HTTPRequestDuration, m.HTTPRequestsInFlight,
		m.HealthLive, m.HealthReady, m.ShipmentsCreated, m.ShipmentStatusTransitions,
		m.ShipmentOperations, m.DeliveryFailures, m.DeliveryDuration, m.TimeToFirstReceipt,
		m.ReturnsCreated, m.ReturnStatusTransitions, m.CallbackRequests, m.CallbackDuration,
		m.OutboxDelivered, m.OutboxRetries, m.APIAuthFailures, m.EmployeeLogins, m.QRScans,
		m.EcommerceDependencyUp,
	)
	return m
}

func SetDefault(m *Metrics) {
	defaultMu.Lock()
	defaultMetrics = m
	defaultMu.Unlock()
}

func Default() *Metrics {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultMetrics
}

type StateCollector struct {
	database         *gorm.DB
	shipmentsCurrent *prometheus.Desc
	returnsCurrent   *prometheus.Desc
	outboxCurrent    *prometheus.Desc
	outboxOldest     *prometheus.Desc
	deliveredToday   *prometheus.Desc
}

func NewStateCollector(database *gorm.DB) *StateCollector {
	return &StateCollector{
		database:         database,
		shipmentsCurrent: prometheus.NewDesc("shipping_shipments_current", "Current shipments by status and type.", []string{"status", "shipment_type"}, nil),
		returnsCurrent:   prometheus.NewDesc("shipping_returns_current", "Current customer returns by status.", []string{"status"}, nil),
		outboxCurrent:    prometheus.NewDesc("shipping_outbox_current", "Current outbox events by status.", []string{"status"}, nil),
		outboxOldest:     prometheus.NewDesc("shipping_outbox_oldest_pending_seconds", "Age of the oldest pending outbox event.", nil, nil),
		deliveredToday:   prometheus.NewDesc("shipping_shipments_delivered_today", "Outbound shipments in delivered status updated since the current UTC day began.", nil, nil),
	}
}

func (collector *StateCollector) Describe(channel chan<- *prometheus.Desc) {
	channel <- collector.shipmentsCurrent
	channel <- collector.returnsCurrent
	channel <- collector.outboxCurrent
	channel <- collector.outboxOldest
	channel <- collector.deliveredToday
}

func (collector *StateCollector) Collect(channel chan<- prometheus.Metric) {
	if collector.database == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var shipmentRows []struct {
		Status   string
		IsReturn bool
		Count    int64
	}
	if collector.database.WithContext(ctx).Model(&models.Shipment{}).Select("status, is_return, count(*) count").Group("status, is_return").Scan(&shipmentRows).Error == nil {
		for _, row := range shipmentRows {
			shipmentType := "outbound"
			if row.IsReturn {
				shipmentType = "return"
			}
			channel <- prometheus.MustNewConstMetric(collector.shipmentsCurrent, prometheus.GaugeValue, float64(row.Count), row.Status, shipmentType)
			if row.IsReturn {
				channel <- prometheus.MustNewConstMetric(collector.returnsCurrent, prometheus.GaugeValue, float64(row.Count), row.Status)
			}
		}
	}
	var delivered int64
	if collector.database.WithContext(ctx).Model(&models.Shipment{}).Where("is_return = ? AND status = ? AND updated_at >= ?", false, models.StatusDelivered, time.Now().UTC().Truncate(24*time.Hour)).Count(&delivered).Error == nil {
		channel <- prometheus.MustNewConstMetric(collector.deliveredToday, prometheus.GaugeValue, float64(delivered))
	}

	var outboxRows []struct {
		Status string
		Count  int64
	}
	if collector.database.WithContext(ctx).Model(&models.OutboxEvent{}).Select("status, count(*) count").Group("status").Scan(&outboxRows).Error == nil {
		for _, row := range outboxRows {
			channel <- prometheus.MustNewConstMetric(collector.outboxCurrent, prometheus.GaugeValue, float64(row.Count), row.Status)
		}
	}
	var oldest models.OutboxEvent
	result := collector.database.WithContext(ctx).Select("created_at").Where("status = ?", models.OutboxStatusPending).Order("created_at ASC").Limit(1).Find(&oldest)
	if result.Error == nil {
		age := 0.0
		if result.RowsAffected > 0 {
			age = max(0, time.Since(oldest.CreatedAt.UTC()).Seconds())
		}
		channel <- prometheus.MustNewConstMetric(collector.outboxOldest, prometheus.GaugeValue, age)
	}
}
