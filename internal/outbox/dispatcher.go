package outbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dispatcher struct {
	database             *gorm.DB
	client               *http.Client
	callbackURL          string
	callbackToken        string
	pollInterval         time.Duration
	retryBaseDelay       time.Duration
	processingStaleAfter time.Duration
	maxAttempts          int
	serviceName          string
}

type Config struct {
	CallbackURL          string
	CallbackToken        string
	PollInterval         time.Duration
	RetryBaseDelay       time.Duration
	ProcessingStaleAfter time.Duration
	MaxAttempts          int
	RequestTimeout       time.Duration
	ServiceName          string
}

func NewDispatcher(database *gorm.DB, configuration Config) *Dispatcher {
	return &Dispatcher{
		database:             database,
		client:               &http.Client{Timeout: configuration.RequestTimeout},
		callbackURL:          configuration.CallbackURL,
		callbackToken:        configuration.CallbackToken,
		pollInterval:         configuration.PollInterval,
		retryBaseDelay:       configuration.RetryBaseDelay,
		processingStaleAfter: configuration.ProcessingStaleAfter,
		maxAttempts:          configuration.MaxAttempts,
		serviceName:          configuration.ServiceName,
	}
}

func (dispatcher *Dispatcher) Enabled() bool {
	return dispatcher.callbackURL != "" && dispatcher.callbackToken != ""
}

func (dispatcher *Dispatcher) Run(ctx context.Context) {
	if !dispatcher.Enabled() {
		return
	}
	ticker := time.NewTicker(dispatcher.pollInterval)
	defer ticker.Stop()
	for {
		if err := dispatcher.DeliverDue(ctx); err != nil && ctx.Err() == nil {
			// best-effort loop; failures remain in the outbox for the next tick
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (dispatcher *Dispatcher) DeliverDue(ctx context.Context) error {
	if !dispatcher.Enabled() {
		return nil
	}
	dueEvents, err := dispatcher.claimDueEvents(ctx, 10)
	if err != nil {
		return err
	}
	for _, dueEvent := range dueEvents {
		if err := dispatcher.deliverOne(ctx, dueEvent); err != nil && ctx.Err() != nil {
			return err
		}
	}
	return nil
}

func (dispatcher *Dispatcher) claimDueEvents(ctx context.Context, limit int) ([]models.OutboxEvent, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-dispatcher.processingStaleAfter)
	var events []models.OutboxEvent
	err := dispatcher.database.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ? AND updated_at <= ?", models.OutboxStatusProcessing, staleBefore).Find(&events).Error; err != nil {
			return err
		}
		if len(events) > 0 {
			ids := make([]uint, 0, len(events))
			for _, event := range events {
				ids = append(ids, event.ID)
			}
			if err := transaction.Model(&models.OutboxEvent{}).Where("id IN ?", ids).Updates(map[string]any{"status": models.OutboxStatusPending, "last_error": "recovered stale processing event", "next_attempt_at": now}).Error; err != nil {
				return err
			}
		}
		events = nil
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ? AND next_attempt_at <= ?", models.OutboxStatusPending, now).Order("next_attempt_at ASC, id ASC").Limit(limit).Find(&events).Error; err != nil {
			return err
		}
		for index := range events {
			event := &events[index]
			if err := transaction.Model(&models.OutboxEvent{}).Where("id = ? AND status = ?", event.ID, models.OutboxStatusPending).Updates(map[string]any{"status": models.OutboxStatusProcessing, "attempts": event.Attempts + 1, "last_error": ""}).Error; err != nil {
				return err
			}
			event.Attempts++
			event.Status = models.OutboxStatusProcessing
		}
		return nil
	})
	return events, err
}

func (dispatcher *Dispatcher) deliverOne(ctx context.Context, event models.OutboxEvent) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, dispatcher.callbackURL, bytes.NewBufferString(event.Payload))
	if err != nil {
		return dispatcher.markRetry(ctx, event, fmt.Errorf("build callback request: %w", err), false)
	}
	request.Header.Set("Authorization", "Bearer "+dispatcher.callbackToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", dispatcher.serviceName+"/1.0")
	request.Header.Set("X-Service-Name", dispatcher.serviceName)
	if event.RequestID != "" {
		request.Header.Set("X-Request-ID", event.RequestID)
	}
	response, err := dispatcher.client.Do(request)
	if err != nil {
		return dispatcher.markRetry(ctx, event, err, isTemporaryError(err))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return dispatcher.markDelivered(ctx, event)
	}
	permanent := response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnprocessableEntity
	return dispatcher.markRetry(ctx, event, fmt.Errorf("callback returned %d", response.StatusCode), !permanent)
}

func (dispatcher *Dispatcher) markDelivered(ctx context.Context, event models.OutboxEvent) error {
	now := time.Now().UTC()
	return dispatcher.database.WithContext(ctx).Model(&models.OutboxEvent{}).Where("id = ?", event.ID).Updates(map[string]any{"status": models.OutboxStatusDelivered, "delivered_at": &now, "last_error": "", "next_attempt_at": now}).Error
}

func (dispatcher *Dispatcher) markRetry(ctx context.Context, event models.OutboxEvent, err error, retryable bool) error {
	status := models.OutboxStatusFailed
	nextAttemptAt := time.Now().UTC()
	if retryable && event.Attempts < dispatcher.maxAttempts {
		status = models.OutboxStatusPending
		nextAttemptAt = nextAttemptAt.Add(backoffDelay(dispatcher.retryBaseDelay, event.Attempts))
	}
	if !retryable {
		status = models.OutboxStatusFailed
	}
	return dispatcher.database.WithContext(ctx).Model(&models.OutboxEvent{}).Where("id = ?", event.ID).Updates(map[string]any{"status": status, "last_error": truncateError(err), "next_attempt_at": nextAttemptAt}).Error
}

func backoffDelay(base time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := base
	for step := 1; step < attempts; step++ {
		delay *= 2
		if delay >= 30*time.Minute {
			return 30 * time.Minute
		}
	}
	return delay
}

func truncateError(err error) string {
	message := err.Error()
	if len(message) > 255 {
		return message[:255]
	}
	return message
}

func isTemporaryError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return true
}
