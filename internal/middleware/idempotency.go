package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	HeaderIdempotencyKey = "X-Idempotency-Key"
	IdempotencyPrefix    = "lock:idempotency:"
	IdempotencyTTL       = 15 * time.Second
)

// DistributedIdempotencyMiddleware enforces atomic deduplication across concurrent tap attempts.
func DistributedIdempotencyMiddleware(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		idempotencyKey := c.GetHeader(HeaderIdempotencyKey)
		if idempotencyKey == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":   "MISSING_IDEMPOTENCY_KEY",
				"message": "Mutating payment requests require an X-Idempotency-Key header",
			})
			return
		}

		redisKey := fmt.Sprintf("%s%s", IdempotencyPrefix, idempotencyKey)

		// Atomic SET NX EX 15s
		acquired, err := rdb.SetNX(c.Request.Context(), redisKey, "PROCESSING", IdempotencyTTL).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":   "IDEMPOTENCY_CHECK_FAILED",
				"message": "Unable to verify distributed idempotency state",
			})
			return
		}

		if !acquired {
			// Key already exists. Check state
			val, _ := rdb.Get(c.Request.Context(), redisKey).Result()
			if val == "PROCESSING" {
				c.AbortWithStatusJSON(http.StatusConflict, gin.H{
					"error":   "CONCURRENT_TAP_DETECTED",
					"message": "A transaction with this idempotency key is currently being cleared. Please wait.",
					"status":  "PROCESSING",
				})
				return
			}

			// If completed response is cached, return cached conflict or status
			c.AbortWithStatusJSON(http.StatusOK, gin.H{
				"message": "Transaction previously processed",
				"cached":  val,
			})
			return
		}

		// Store key in context for release or update
		c.Set("idempotency_redis_key", redisKey)
		c.Next()
	}
}

// ReleaseIdempotencyLock releases the lock if an early failure occurred before settlement.
func ReleaseIdempotencyLock(ctx context.Context, rdb *redis.Client, redisKey string) {
	if redisKey != "" {
		_ = rdb.Del(ctx, redisKey).Err()
	}
}

// CacheIdempotencyResult persists the transaction result for the remaining TTL.
func CacheIdempotencyResult(ctx context.Context, rdb *redis.Client, redisKey string, resultJSON string) {
	if redisKey != "" {
		_ = rdb.Set(ctx, redisKey, resultJSON, IdempotencyTTL).Err()
	}
}

