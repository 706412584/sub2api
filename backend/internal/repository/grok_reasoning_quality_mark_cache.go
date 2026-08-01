package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const grokReasoningQualityMarkPrefix = "proxy:grok_reasoning:"

type grokReasoningQualityMarkCache struct {
	rdb *redis.Client
}

// NewGrokReasoningQualityMarkCache stores Grok reasoning quality marks in Redis.
func NewGrokReasoningQualityMarkCache(rdb *redis.Client) service.GrokReasoningQualityMarkStore {
	return &grokReasoningQualityMarkCache{rdb: rdb}
}

func grokReasoningQualityMarkKey(proxyID int64) string {
	return fmt.Sprintf("%s%d", grokReasoningQualityMarkPrefix, proxyID)
}

func (c *grokReasoningQualityMarkCache) Set(ctx context.Context, mark *service.GrokReasoningQualityMark, ttl time.Duration) error {
	if c == nil || c.rdb == nil || mark == nil || mark.ProxyID <= 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = service.GrokReasoningQualityMarkTTL
	}
	cp := *mark
	if cp.ProbedAt == 0 {
		cp.ProbedAt = time.Now().Unix()
	}
	cp.ExpiresAt = time.Now().Add(ttl).Unix()
	raw, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("marshal grok reasoning quality mark: %w", err)
	}
	return c.rdb.Set(ctx, grokReasoningQualityMarkKey(cp.ProxyID), raw, ttl).Err()
}

func (c *grokReasoningQualityMarkCache) Get(ctx context.Context, proxyID int64) (*service.GrokReasoningQualityMark, error) {
	if c == nil || c.rdb == nil || proxyID <= 0 {
		return nil, nil
	}
	val, err := c.rdb.Get(ctx, grokReasoningQualityMarkKey(proxyID)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var mark service.GrokReasoningQualityMark
	if err := json.Unmarshal([]byte(val), &mark); err != nil {
		return nil, fmt.Errorf("unmarshal grok reasoning quality mark: %w", err)
	}
	if mark.ExpiresAt > 0 && time.Now().Unix() >= mark.ExpiresAt {
		_ = c.rdb.Del(ctx, grokReasoningQualityMarkKey(proxyID)).Err()
		return nil, nil
	}
	return &mark, nil
}

func (c *grokReasoningQualityMarkCache) Delete(ctx context.Context, proxyID int64) error {
	if c == nil || c.rdb == nil || proxyID <= 0 {
		return nil
	}
	return c.rdb.Del(ctx, grokReasoningQualityMarkKey(proxyID)).Err()
}
