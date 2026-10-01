package billingexpr

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCNOffPeakTimeBoundariesHolidaysAndMakeupWeekend(t *testing.T) {
	calendar := newCNHolidayCache()
	for _, tc := range []struct {
		at      string
		offPeak bool
	}{
		{"2026-09-30T08:59:59+08:00", true}, {"2026-09-30T09:00:00+08:00", false},
		{"2026-09-30T11:59:59+08:00", false}, {"2026-09-30T12:00:00+08:00", true},
		{"2026-09-30T13:59:59+08:00", true}, {"2026-09-30T14:00:00+08:00", false},
		{"2026-09-30T17:59:59+08:00", false}, {"2026-09-30T18:00:00+08:00", true},
		{"2026-10-01T10:00:00+08:00", true}, {"2026-10-10T10:00:00+08:00", true},
		{"2026-09-30T01:00:00Z", false},
	} {
		t.Run(tc.at, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			require.NoError(t, err)
			got, err := calendar.offPeak(at)
			require.NoError(t, err)
			assert.Equal(t, tc.offPeak, got)
		})
	}
}

func TestCNCalendarMissingYearDoesNotGuessAndFetchFailuresKeepLastValidData(t *testing.T) {
	calendar := newCNHolidayCache()
	calendar.fetch = func(context.Context, int) ([]byte, error) { return nil, fmt.Errorf("offline") }
	_, err := calendar.offPeak(time.Date(2027, 1, 4, 10, 0, 0, 0, chinaPricingZone))
	require.Error(t, err)
	_, err = calendar.year(2026, true)
	require.Error(t, err)
	off, err := calendar.offPeak(time.Date(2026, 10, 1, 10, 0, 0, 0, chinaPricingZone))
	require.NoError(t, err)
	assert.True(t, off)
	for _, invalid := range []string{`{}`, `{"year":2027,"days":[]}`, `{"year":2026,"papers":["https://example.com"],"days":[{"date":"2026-10-01","isOffDay":true}]}`} {
		_, err := parseCNHolidayCalendar([]byte(invalid), 2026)
		require.Error(t, err)
	}
}

func TestCNTimePricingSettlementKeepsRequestStartBand(t *testing.T) {
	expression := `cn_off_peak() ? tier("off_peak", p * 4.5 + c * 13.5 + cr * 0.15) : tier("peak", p * 9 + c * 27 + cr * 0.3)`
	start := time.Date(2026, 9, 30, 8, 59, 59, 0, chinaPricingZone)
	finish := start.Add(time.Minute)
	snapshot := &BillingSnapshot{ExprString: expression, ExprHash: ExprHashString(expression), GroupRatio: 1, QuotaPerUnit: 1000000, PricingTime: start, EstimatedTier: "off_peak"}
	result, err := ComputeTieredQuotaWithRequest(snapshot, TokenParams{P: 1, C: 1, CR: 1}, RequestInput{PricingTime: finish})
	require.NoError(t, err)
	assert.Equal(t, "off_peak", result.MatchedTier)
	assert.InDelta(t, 18.15, result.ActualQuotaBeforeGroup, 1e-12)
	assert.False(t, result.CrossedTier)
}

func TestCNCalendarLoadsAndRetainsNextYearHolidays(t *testing.T) {
	calendar := newCNHolidayCache()
	calendar.fetch = func(_ context.Context, year int) ([]byte, error) {
		require.Equal(t, 2027, year)
		// Explicit fixture, not a prediction of the actual 2027 schedule.
		return []byte(`{"year":2027,"papers":["https://www.gov.cn/calendar-test"],"days":[{"date":"2027-01-04","isOffDay":true}]}`), nil
	}
	off, err := calendar.offPeak(time.Date(2027, 1, 4, 10, 0, 0, 0, chinaPricingZone))
	require.NoError(t, err)
	assert.True(t, off)
	calendar.fetch = func(context.Context, int) ([]byte, error) { return nil, fmt.Errorf("offline") }
	off, err = calendar.offPeak(time.Date(2027, 1, 4, 14, 0, 0, 0, chinaPricingZone))
	require.NoError(t, err)
	assert.True(t, off)
	off, err = calendar.offPeak(time.Date(2027, 1, 5, 10, 0, 0, 0, chinaPricingZone))
	require.NoError(t, err)
	assert.False(t, off)
}
