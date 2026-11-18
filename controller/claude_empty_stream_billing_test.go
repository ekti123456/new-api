package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeCanceledStreamSettlementAndReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupPreConsumePolicyTest(t)
	// Initialize SQL quoting against this in-memory database, never an external DSN.
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	previousLogs, previousExport := common.LogConsumeEnabled, common.DataExportEnabled
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	t.Cleanup(func() { common.LogConsumeEnabled, common.DataExportEnabled = previousLogs, previousExport })
	for i, tc := range []struct {
		name, expr                 string
		trusted, invalidSettlement bool
		upstreamUsage              bool
		cacheOnly, partialOutput   bool
		wantCharge                 int
	}{
		{name: "refund reservation", expr: `tier("base", p * 4 + c * 20)`},
		{name: "trusted wallet stays unchanged", expr: `tier("base", p * 4 + c * 20)`, trusted: true},
		{name: "empty response skips fixed expression fee", expr: `tier("base", 1000000)`},
		{name: "empty response cannot retain estimate on pricing failure", expr: `tier("base", p * 4 + c * 20)`, invalidSettlement: true},
		{name: "measured usage still billed", expr: `tier("base", p * 4 + c * 20 + cr * 0.4)`, upstreamUsage: true, wantCharge: 4500},
		{name: "cache-only usage updates balances and statistics", expr: `tier("base", p * 4 + c * 20 + cr * 0.4)`, upstreamUsage: true, cacheOnly: true, wantCharge: 1500},
		{name: "partial output merged with measured cache usage", expr: `tier("base", p * 4 + c * 20 + cr * 0.4)`, upstreamUsage: true, partialOutput: true, wantCharge: 4500 + service.EstimateTokenByModel("claude-opus-5-5", "reply ok")*15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing, err := common.Marshal(map[string]string{"claude-empty-stream": tc.expr})
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
				"billing_setting.billing_mode":         `{"claude-empty-stream":"tiered_expr"}`,
				"billing_setting.billing_expr":         string(pricing),
				"group_ratio_setting.group_ratio":      `{"default":1.5}`,
				"quota_setting.pre_consume_multiplier": "0.1",
				"quota_setting.trust_quota_usd":        "0",
			}))
			if tc.trusted {
				operation_setting.GetQuotaSetting().TrustQuotaUSD = 10
			}
			const startingQuota = 10000000
			user := model.User{Username: fmt.Sprintf("empty-stream-user-%d", i), Quota: startingQuota, Group: "default", AffCode: fmt.Sprintf("empty-stream-%d", i)}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("empty-stream-token-%d", i), RemainQuota: startingQuota}
			require.NoError(t, db.Create(&token).Error)
			channel := model.Channel{Name: "empty-stream-test"}
			require.NoError(t, db.Create(&channel).Error)
			requestCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)
			ctx.Set("token_quota", startingQuota)
			info := &relaycommon.RelayInfo{
				UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
				RelayFormat: types.RelayFormatClaude, IsStream: true, DisablePing: true,
				OriginModelName: "claude-empty-stream", UserGroup: "default", UsingGroup: "default",
				BillingRequestInput: &billingexpr.RequestInput{}, StartTime: time.Now(),
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id, UpstreamModelName: "claude-opus-5-5"},
			}
			info.UserSetting.BillingPreference = "wallet_only"
			info.SetEstimatePromptTokens(2014151)
			price, err := helper.ModelPriceHelper(ctx, info, info.GetEstimatePromptTokens(), &types.TokenCountMeta{})
			require.NoError(t, err)
			require.Nil(t, service.PreConsumeBilling(ctx, price.QuotaToPreConsume, info))
			if tc.trusted {
				assert.Zero(t, info.FinalPreConsumedQuota)
			} else {
				assert.Positive(t, info.FinalPreConsumedQuota)
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, startingQuota-info.FinalPreConsumedQuota, user.Quota)
			assert.Equal(t, startingQuota-info.FinalPreConsumedQuota, token.RemainQuota)
			if tc.invalidSettlement {
				info.TieredBillingSnapshot.ExprString = "("
				info.TieredBillingSnapshot.ExprHash = billingexpr.ExprHashString("(")
			}
			var usage *dto.Usage
			if tc.upstreamUsage {
				state := &claude.ClaudeResponseInfo{Usage: &dto.Usage{}}
				input := 1000
				if tc.cacheOnly {
					input = 0
				}
				require.Nil(t, claude.HandleStreamResponseData(ctx, info, state, fmt.Sprintf(`{"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":%d,"output_tokens":0,"cache_read_input_tokens":5000}}}`, input)))
				if tc.partialOutput {
					require.Nil(t, claude.HandleStreamResponseData(ctx, info, state, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"reply ok"}}`))
				}
				cancel()
				info.StreamStatus = relaycommon.NewStreamStatus()
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
				claude.HandleStreamFinalResponse(ctx, info, state)
				usage = state.Usage
			} else {
				reader, writer := io.Pipe()
				t.Cleanup(func() { _ = writer.Close() })
				cancel()
				var apiErr *types.NewAPIError
				usage, apiErr = claude.ClaudeStreamHandler(ctx, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}, info)
				require.Nil(t, apiErr)
			}
			service.PostTextConsumeQuota(ctx, info, usage, nil)
			// Retried settlement or the outer error defer must not refund twice.
			require.NoError(t, info.Billing.Settle(tc.wantCharge))
			info.Billing.Refund(ctx)
			assert.False(t, info.Billing.NeedsRefund())
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			assert.Equal(t, startingQuota-tc.wantCharge, user.Quota)
			assert.Equal(t, startingQuota-tc.wantCharge, token.RemainQuota)
			assert.Equal(t, tc.wantCharge, user.UsedQuota)
			assert.Equal(t, tc.wantCharge, token.UsedQuota)
			assert.Equal(t, int64(tc.wantCharge), channel.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, tc.wantCharge, logs[0].Quota)
			if !tc.upstreamUsage {
				assert.Zero(t, logs[0].PromptTokens)
				assert.Zero(t, logs[0].CompletionTokens)
				assert.Contains(t, logs[0].Content, "本次未扣费")
			} else {
				assert.NotContains(t, logs[0].Content, "本次未扣费")
			}
			var other struct {
				Admin struct {
					BillingPath string `json:"usage_billing_path"`
				} `json:"admin_info"`
				Stream struct {
					Status    string `json:"status"`
					EndReason string `json:"end_reason"`
					EndError  string `json:"end_error"`
				} `json:"stream_status"`
			}
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Equal(t, "error", other.Stream.Status)
			assert.Equal(t, "client_gone", other.Stream.EndReason)
			assert.Equal(t, "context canceled", other.Stream.EndError)
			if tc.partialOutput {
				assert.Equal(t, "local", other.Admin.BillingPath)
				assert.Equal(t, service.EstimateTokenByModel("claude-opus-5-5", "reply ok"), logs[0].CompletionTokens)
			} else if tc.upstreamUsage {
				assert.Equal(t, "billing-usage-anthropic", other.Admin.BillingPath)
			}
		})
	}
}
