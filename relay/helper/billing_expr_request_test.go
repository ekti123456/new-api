package helper

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveIncomingBillingExprRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")

	body := []byte(`{"service_tier":"fast"}`)
	ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
	ctx.Set(common.KeyRequestBody, body)

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{"Content-Type": "application/json"},
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelId: 15},
	}
	ctx.Set("channel_id", 12)

	input, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	require.Equal(t, body, input.Body)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
	require.Equal(t, 12, input.ChannelID)
	info.BillingRequestInput = &input
	ctx.Set("channel_id", 15)
	updated, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	require.Equal(t, 15, updated.ChannelID)
	require.Equal(t, 12, input.ChannelID)
}

func TestBuildBillingExprRequestInputFromRequest(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:  "gemini-3.1-pro-preview",
		Stream: lo.ToPtr(true),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hi",
			},
		},
		MaxTokens: lo.ToPtr(uint(3000)),
	}

	input, err := BuildBillingExprRequestInputFromRequest(request, map[string]string{
		"Content-Type": "application/json",
		"X-Test":       "1",
	})
	require.NoError(t, err)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
	require.Equal(t, "1", input.Headers["X-Test"])
	require.True(t, gjson.GetBytes(input.Body, "stream").Bool())
	require.Equal(t, "user", gjson.GetBytes(input.Body, "messages.0.role").String())
	require.Equal(t, float64(3000), gjson.GetBytes(input.Body, "max_tokens").Float())
}

func TestBillingRequestCapturesAcceptedJSONDespiteMissingContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain", "application/octet-stream"} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"service_tier":"priority"}`))
		ctx.Request.Header.Set("Content-Type", contentType)
		info := &relaycommon.RelayInfo{Request: &dto.OpenAIResponsesRequest{}}
		input, err := ResolveIncomingBillingExprRequestInput(ctx, info)
		require.NoError(t, err)
		require.Equal(t, "incoming_json_inferred", input.BodySource)
		require.Equal(t, contentType, input.ContentType)
		_, trace, err := billingexpr.RunExprWithRequest(`tier("base", p) * (param("service_tier") == "priority" ? 2 : 1)`, billingexpr.TokenParams{P: 10}, input)
		require.NoError(t, err)
		require.True(t, trace.RequestRuleMatches[0].Matched)
		diagnostic := relaycommon.CaptureBillingRequestDiagnostic(&input)
		require.NotNil(t, diagnostic.ServiceTier.Value)
		require.Equal(t, "priority", *diagnostic.ServiceTier.Value)
	}
}

func TestBillingTierSnapshotDistinguishesOriginalValueFromOutboundFiltering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousPassthrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = previousPassthrough })
	for _, sample := range []struct {
		name, body, contentType, state, source string
		matched                                bool
	}{
		{"priority", `{"service_tier":"priority"}`, "application/json", "string", "incoming_json", true},
		{"fast alias is not priority", `{"service_tier":"fast"}`, "application/json; charset=utf-8", "string", "incoming_json", false},
		{"missing", `{}`, "application/json", "absent", "incoming_json", false},
		{"body skipped by content type", `{"service_tier":"priority"}`, "text/plain", "unavailable", "content_type_not_json", false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(sample.body))
			ctx.Request.Header.Set("Content-Type", sample.contentType)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}
			input, err := ResolveIncomingBillingExprRequestInput(ctx, info)
			require.NoError(t, err)
			outbound, err := relaycommon.RemoveDisabledFields([]byte(sample.body), dto.ChannelOtherSettings{}, false)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(outbound, "service_tier").Exists())
			_, trace, err := billingexpr.RunExprWithRequest(`tier("base", p) * (param("service_tier") == "priority" ? 2 : 1)`, billingexpr.TokenParams{P: 10}, input)
			require.NoError(t, err)
			require.Len(t, trace.RequestRuleMatches, 1)
			require.Equal(t, sample.matched, trace.RequestRuleMatches[0].Matched)
			diagnostic := relaycommon.CaptureBillingRequestDiagnostic(&input)
			require.Equal(t, sample.state, diagnostic.ServiceTier.State)
			require.Equal(t, sample.source, diagnostic.Source)
			info.BillingRequestInput = &input
			ctx.Request.Header.Set("Content-Type", "application/octet-stream")
			reused, err := ResolveIncomingBillingExprRequestInput(ctx, info)
			require.NoError(t, err)
			require.Equal(t, input.ContentType, reused.ContentType, "frozen request provenance must survive retries")
		})
	}
}
