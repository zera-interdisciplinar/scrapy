package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ponytail: fixed-window counter per IP, in-process map. Fine at scrapy's traffic; swap
// for a shared store (Redis) if this ever runs with >1 replica and needs a shared limit.
type rateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	limit    int
	counters map[string]*windowCount
}

type windowCount struct {
	resetAt time.Time
	count   int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, counters: map[string]*windowCount{}}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	c, ok := rl.counters[key]
	if !ok || now.After(c.resetAt) {
		c = &windowCount{resetAt: now.Add(rl.window)}
		rl.counters[key] = c
	}
	c.count++
	return c.count <= rl.limit
}

func (rl *rateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
