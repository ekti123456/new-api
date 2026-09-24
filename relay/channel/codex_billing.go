package channel

import (
	"net/http"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/tidwall/gjson"
)

func codexBillingTier(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "fast":
		return "priority"
	case "priority", "ultrafast", "default", "standard", "auto", "flex", "scale":
		return value
	default:
		return ""
	}
}

func newCodexBillingObserver(resp *http.Response, info *relaycommon.RelayInfo) func([]byte, string) {
	if info == nil || info.ChannelMeta == nil || resp == nil || resp.StatusCode != http.StatusOK || resp.Request == nil || resp.Request.URL == nil {
		return nil
	}
	request, ok := resp.Request.Context().Value(newAPIPolicyRequestContextKey{}).(newAPIPolicyRequestContext)
	if !ok || request.Secret == "" || request.RequestID == "" || request.ChannelID <= 0 || request.UserID <= 0 || request.RequestID != info.RequestId || request.ChannelID != info.ChannelId || request.UserID != info.UserId || !IsCodex2APIPolicyDestination(resp.Request.URL.String(), info.ApiKey) {
		return nil
	}
	observation := &relaycommon.CodexBillingObservation{}
	info.CodexBilling = observation
	return func(data []byte, event string) {
		if !gjson.ValidBytes(data) {
			return
		}
		root := gjson.ParseBytes(data)
		kind := root.Get("type").String()
		if kind == "" {
			kind = event
		}
		response := root
		if kind != "" {
			if kind != "response.completed" && kind != "response.done" && kind != "response.incomplete" {
				return
			}
			response = root.Get("response")
		}
		if !response.IsObject() || response.Get("error").IsObject() {
			return
		}
		switch response.Get("status").String() {
		case "failed", "cancelled", "canceled":
			return
		}
		report := relaycommon.CodexBillingReport{}
		if metadata := response.Get("codex2api_billing"); metadata.Exists() {
			if !metadata.IsObject() || metadata.Get("version").Float() != 1 || metadata.Get("version").Type != gjson.Number {
				return
			}
			report.ServiceTier = codexBillingTier(metadata.Get("service_tier").String())
			report.Source = metadata.Get("source").String()
			if report.Source != "upstream_response" && report.Source != "effective_request" {
				return
			}
			report.RequestedServiceTier = codexBillingTier(metadata.Get("requested_service_tier").String())
			report.ActualServiceTier = codexBillingTier(metadata.Get("actual_service_tier").String())
			report.LocalBillingServiceTier = codexBillingTier(metadata.Get("local_billing_service_tier").String())
			if (report.Source == "upstream_response" && report.ServiceTier != report.ActualServiceTier) || (report.Source == "effective_request" && (report.ServiceTier != report.RequestedServiceTier || report.ActualServiceTier != "")) {
				return
			}
			report.Protocol = "codex2api_billing_v1"
		} else {
			// Legacy Responses and Chat gateways already return service_tier.
			// Only accept response envelopes, never nested tools or user text.
			if kind == "" && response.Get("object").String() != "response" && response.Get("object").String() != "response.compaction" && !response.Get("choices").IsArray() {
				return
			}
			report.ServiceTier = codexBillingTier(response.Get("service_tier").String())
			report.ActualServiceTier, report.Source, report.Protocol = report.ServiceTier, "upstream_response", "legacy_service_tier"
		}
		if report.ServiceTier != "" {
			observation.Record(report)
		}
	}
}
