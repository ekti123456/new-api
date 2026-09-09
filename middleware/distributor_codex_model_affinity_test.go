package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestCodexModelSwitchKeepsChannelBindingAndRequiresNewConversation(test *testing.T) {
	channel, key, keyFingerprint := setupCodexRootDistributorTest(test)
	priority := int64(1)
	baseURL := channel.GetBaseURL()
	fallback := &model.Channel{
		Id: channel.Id + 121, Type: constant.ChannelTypeOpenAI, Key: key, Status: common.ChannelStatusEnabled,
		Name: "other-model-channel", BaseURL: &baseURL, Models: "gpt-5.6-terra", Group: "pro", Priority: &priority,
	}
	require.NoError(test, model.DB.Create(fallback).Error)
	require.NoError(test, model.DB.Create(&model.Ability{Group: "pro", Model: "gpt-5.6-terra", ChannelId: fallback.Id, Enabled: true, Priority: &priority}).Error)
	model.InitChannelCache()
	const rootID = "01a06000-0000-7000-8000-000000000971"
	binding := service.CodexRootChannelBinding{ChannelID: channel.Id, SelectedGroup: "pro", KeyIndex: 0, KeyFingerprint: keyFingerprint}
	require.NoError(test, service.StoreCodexRootChannelBinding(42, rootID, binding))
	requestContext, recorder := codexMainRootContext(42, 971, 0, rootID)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-terra","input":"switch model"}`))
	request.Header = requestContext.Request.Header
	requestContext.Request = request
	Distribute()(requestContext)
	require.Equal(test, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(test, recorder.Body.String(), "session_model_unavailable")
	require.Contains(test, recorder.Body.String(), "请新开对话")
	require.Zero(test, common.GetContextKeyInt(requestContext, constant.ContextKeyChannelId))
	stored, found, err := service.LoadCodexRootChannelBinding(42, rootID)
	require.NoError(test, err)
	require.True(test, found)
	require.Equal(test, channel.Id, stored.ChannelID)
	require.Equal(test, keyFingerprint, stored.KeyFingerprint)

	original, originalRecorder := codexMainRootContext(42, 971, 0, rootID)
	Distribute()(original)
	require.Less(test, originalRecorder.Code, http.StatusBadRequest, originalRecorder.Body.String())
	require.Equal(test, channel.Id, common.GetContextKeyInt(original, constant.ContextKeyChannelId))

	fresh, freshRecorder := codexMainRootContext(42, 971, 0, "01a06000-0000-7000-8000-000000000972")
	freshRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-terra","input":"new conversation"}`))
	freshRequest.Header = fresh.Request.Header
	fresh.Request = freshRequest
	Distribute()(fresh)
	require.Less(test, freshRecorder.Code, http.StatusBadRequest, freshRecorder.Body.String())
	require.Equal(test, fallback.Id, common.GetContextKeyInt(fresh, constant.ContextKeyChannelId))
}
