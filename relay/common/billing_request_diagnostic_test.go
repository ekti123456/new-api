package common

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRequestDiagnosticPreservesExactTierAndRedactsUnrelatedData(t *testing.T) {
	for _, sample := range []struct {
		name, body, state, value string
		redacted                 bool
	}{
		{"priority", `{"service_tier":"priority"}`, "string", "priority", false},
		{"case and whitespace", `{"service_tier":" Priority "}`, "string", " Priority ", false},
		{"empty string", `{"service_tier":""}`, "string", "", false},
		{"null differs from absent", `{"service_tier":null}`, "null", "", false},
		{"absent", `{}`, "absent", "", false},
		{"object is not a tier", `{"service_tier":{"value":"private-argument"}}`, "object", "", false},
		{"arbitrary string", `{"service_tier":"private-credential"}`, "string", "", true},
		{"oversize string", `{"service_tier":"` + strings.Repeat("priority", 20) + `"}`, "string", "", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			d := CaptureBillingRequestDiagnostic(&billingexpr.RequestInput{Body: []byte(sample.body), Headers: map[string]string{"Authorization": "Bearer private-credential"}})
			assert.Equal(t, sample.state, d.ServiceTier.State)
			assert.Equal(t, sample.redacted, d.ServiceTier.Redacted)
			if sample.state == "string" && !sample.redacted {
				require.NotNil(t, d.ServiceTier.Value)
				assert.Equal(t, sample.value, *d.ServiceTier.Value)
			} else {
				assert.Nil(t, d.ServiceTier.Value)
			}
			encoded, err := common.Marshal(d)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private-credential")
			assert.NotContains(t, string(encoded), "private-argument")
		})
	}
	d := CaptureBillingRequestDiagnostic(&billingexpr.RequestInput{Body: []byte(`{"serviceTier":"priority","input":"private-prompt"}`)})
	assert.Equal(t, "absent", d.ServiceTier.State)
	require.NotNil(t, d.ServiceTierAlias)
	assert.Equal(t, "priority", *d.ServiceTierAlias.Value)
	assert.Equal(t, "unavailable", CaptureBillingRequestDiagnostic(nil).BodyState)
	assert.Equal(t, "invalid_json", CaptureBillingRequestDiagnostic(&billingexpr.RequestInput{Body: []byte(`{`)}).BodyState)
}
