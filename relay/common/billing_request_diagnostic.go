package common

import (
	"mime"
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/tidwall/gjson"
)

// A bounded admin-only observation of the exact input used for settlement.
// It deliberately excludes prompts, tools, authorization and arbitrary values.
type BillingRequestDiagnostic struct {
	Source               string                      `json:"source"`
	ContentType          string                      `json:"content_type"`
	BodyState            string                      `json:"body_state"`
	ServiceTier          BillingTierFieldDiagnostic  `json:"service_tier"`
	ServiceTierAlias     *BillingTierFieldDiagnostic `json:"service_tier_camel_case,omitempty"`
	Codex2API            *CodexBillingReport         `json:"codex2api,omitempty"`
	EffectiveServiceTier string                      `json:"effective_service_tier,omitempty"`
	PriorityMatchSource  string                      `json:"priority_match_source"`
}

type BillingTierFieldDiagnostic struct {
	State    string  `json:"state"`
	Value    *string `json:"value,omitempty"`
	Redacted bool    `json:"redacted,omitempty"`
}

func CaptureBillingRequestDiagnostic(input *billingexpr.RequestInput) *BillingRequestDiagnostic {
	d := &BillingRequestDiagnostic{Source: "snapshot_unavailable", ContentType: "not_recorded", BodyState: "unavailable", ServiceTier: BillingTierFieldDiagnostic{State: "unavailable"}}
	if input == nil {
		return d
	}
	d.Source = input.BodySource
	if d.Source == "" {
		d.Source = "provided_snapshot"
	}
	if d.Source == "incoming_json" || d.Source == "incoming_json_inferred" || d.Source == "content_type_not_json" {
		d.ContentType = "missing"
	}
	if input.ContentType != "" {
		d.ContentType = "other_or_invalid"
		if mediaType, _, err := mime.ParseMediaType(input.ContentType); err == nil {
			switch mediaType {
			case "application/json", "application/octet-stream", "text/plain", "multipart/form-data", "application/x-www-form-urlencoded":
				d.ContentType = mediaType
			}
		}
	}
	if len(input.Body) == 0 {
		return d
	}
	d.BodyState = "invalid_json"
	if !gjson.ValidBytes(input.Body) {
		return d
	}
	d.BodyState = "json"
	tier := gjson.GetBytes(input.Body, "service_tier")
	d.ServiceTier = billingTierFieldDiagnostic(tier)
	if alias := gjson.GetBytes(input.Body, "serviceTier"); alias.Exists() {
		field := billingTierFieldDiagnostic(alias)
		d.ServiceTierAlias = &field
	}
	return d
}

func billingTierFieldDiagnostic(value gjson.Result) BillingTierFieldDiagnostic {
	if !value.Exists() {
		return BillingTierFieldDiagnostic{State: "absent"}
	}
	field := BillingTierFieldDiagnostic{State: strings.ToLower(value.Type.String())}
	if value.IsArray() {
		field.State = "array"
	} else if value.IsObject() {
		field.State = "object"
	}
	if value.Type != gjson.String {
		return field
	}
	text := value.String()
	// Preserve case and whitespace for known tier values so exact-match misses
	// remain visible. Do not log arbitrary user strings placed in this field.
	if len(text) <= 64 {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "", "priority", "fast", "ultrafast", "auto", "default", "flex", "scale", "standard":
			field.Value = &text
			return field
		}
	}
	field.Redacted = true
	return field
}
