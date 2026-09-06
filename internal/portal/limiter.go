package portal

import (
	"sync"
	"time"
)

type loginAttempt struct {
	count int
	reset time.Time
}
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	limit    int
	window   time.Duration
}

func NewLoginLimiter(limit int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{attempts: make(map[string]loginAttempt), limit: limit, window: window}
}

func (limiter *LoginLimiter) Allow(key string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	if len(limiter.attempts) > 10_000 {
		for candidate, attempt := range limiter.attempts {
			if now.After(attempt.reset) {
				delete(limiter.attempts, candidate)
			}
		}
	}
	attempt := limiter.attempts[key]
	if now.After(attempt.reset) {
		attempt = loginAttempt{reset: now.Add(limiter.window)}
	}
	if attempt.count >= limiter.limit {
		return false
	}
	attempt.count++
	limiter.attempts[key] = attempt
	return true
}

func (limiter *LoginLimiter) Reset(key string) {
	limiter.mu.Lock()
	delete(limiter.attempts, key)
	limiter.mu.Unlock()
}
