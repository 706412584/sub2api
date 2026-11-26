package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 每次 Forward 返回后先取走摘要用量，不能等到成功路径或换号后才归属账号。
func (h *GatewayHandler) submitTurnLimitSummaryUsage(c *gin.Context, apiKey *service.APIKey, account *service.Account, subscription *service.UserSubscription, mapping service.ChannelMappingResult, model string, pricingAt time.Time) {
	summary := service.TakeTurnLimitSummaryUsage(c)
	if summary == nil {
		return
	}
	input := &service.RecordUsageInput{
		Result: summary.ForwardResult(), APIKey: apiKey, User: apiKey.User,
		Account: account, Subscription: subscription, PricingAt: pricingAt,
		InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: summary.UpstreamEndpoint,
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		SessionID: service.ExtractClientSessionID(c), RequestPayloadHash: summary.RequestPayloadHash,
		APIKeyService: h.apiKeyService, QuotaPlatform: service.QuotaPlatform(c.Request.Context(), apiKey),
		ChannelUsageFields: clientRequestedUsageFields(c, mapping, model, summary.Model),
	}
	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, input); err != nil {
			logger.L().Error("gateway.turn_limit_summary.record_usage_failed", zap.Int64("account_id", input.Account.ID), zap.Error(err))
		}
	})
}

func (h *OpenAIGatewayHandler) submitTurnLimitSummaryUsage(c *gin.Context, apiKey *service.APIKey, account *service.Account, subscription *service.UserSubscription, mapping service.ChannelMappingResult, model string, pricingAt time.Time) {
	summary := service.TakeTurnLimitSummaryUsage(c)
	if summary == nil {
		return
	}
	input := &service.OpenAIRecordUsageInput{
		Result: &service.OpenAIForwardResult{
			RequestID: summary.RequestID, Model: summary.Model, UpstreamModel: summary.Model,
			UpstreamEndpoint: summary.UpstreamEndpoint, Usage: summary.Usage, Stream: true, Duration: summary.Duration,
		},
		APIKey: apiKey, User: apiKey.User, Account: account, Subscription: subscription, PricingAt: pricingAt,
		InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: summary.UpstreamEndpoint,
		UserAgent: c.GetHeader("User-Agent"), IPAddress: ip.GetClientIP(c),
		SessionID: service.ExtractClientSessionID(c), RequestPayloadHash: summary.RequestPayloadHash,
		APIKeyService: h.apiKeyService, QuotaPlatform: service.QuotaPlatform(c.Request.Context(), apiKey),
		ChannelUsageFields: clientRequestedUsageFields(c, mapping, model, summary.Model),
	}
	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordUsage(ctx, input); err != nil {
			logger.L().Error("openai.turn_limit_summary.record_usage_failed", zap.Int64("account_id", input.Account.ID), zap.Error(err))
		}
	})
}
