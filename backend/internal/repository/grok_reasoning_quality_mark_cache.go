package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// Account-keyed mark store. Legacy proxy-keyed keys (proxy:grok_reasoning:*) are
// intentionally not read: probe outcomes vary primarily by account credential.
const grokReasoningQualityMarkPrefix = "account:grok_reasoning:"

type grokReasoningQualityMarkCache struct {
	rdb *redis.Client
}

// NewGrokReasoningQualityMarkCache stores Grok reasoning quality marks in Redis.
func NewGrokReasoningQualityMarkCache(rdb *redis.Client) service.GrokReasoningQualityMarkStore {
	return &grokReasoningQualityMarkCache{rdb: rdb}
}

func grokReasoningQualityMarkKey(accountID int64) string {
	return fmt.Sprintf("%s%d", grokReasoningQualityMarkPrefix, accountID)
}

func (c *grokReasoningQualityMarkCache) Set(ctx context.Context, mark *service.GrokReasoningQualityMark, ttl time.Duration) error {
	if c == nil || c.rdb == nil || mark == nil || mark.AccountID <= 0 {
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
	return c.rdb.Set(ctx, grokReasoningQualityMarkKey(cp.AccountID), raw, ttl).Err()
}

func (c *grokReasoningQualityMarkCache) Get(ctx context.Context, accountID int64) (*service.GrokReasoningQualityMark, error) {
	if c == nil || c.rdb == nil || accountID <= 0 {
		return nil, nil
	}
	val, err := c.rdb.Get(ctx, grokReasoningQualityMarkKey(accountID)).Result()
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
		_ = c.rdb.Del(ctx, grokReasoningQualityMarkKey(accountID)).Err()
		return nil, nil
	}
	return &mark, nil
}

func (c *grokReasoningQualityMarkCache) Delete(ctx context.Context, accountID int64) error {
	if c == nil || c.rdb == nil || accountID <= 0 {
		return nil
	}
	return c.rdb.Del(ctx, grokReasoningQualityMarkKey(accountID)).Err()
}
