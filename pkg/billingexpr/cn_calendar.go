package billingexpr

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Source: NateScarlet/holiday-cn, sourced from State Council announcements.
// The bundled snapshot and upstream MIT license are in calendar/.
//
//go:embed calendar/2026.json
var embeddedCNCalendar []byte

var chinaPricingZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type cnHolidayCache struct {
	mu          sync.RWMutex
	fetchMu     sync.Mutex
	years       map[int]map[string]bool
	lastAttempt map[int]time.Time
	fetch       func(context.Context, int) ([]byte, error)
}

var pricingHolidays = newCNHolidayCache()

func newCNHolidayCache() *cnHolidayCache {
	days, err := parseCNHolidayCalendar(embeddedCNCalendar, 2026)
	if err != nil {
		panic(err)
	}
	return &cnHolidayCache{years: map[int]map[string]bool{2026: days}, lastAttempt: map[int]time.Time{2026: time.Now()}, fetch: fetchCNHolidayCalendar}
}

func parseCNHolidayCalendar(body []byte, year int) (map[string]bool, error) {
	var data struct {
		Year   int      `json:"year"`
		Papers []string `json:"papers"`
		Days   []struct {
			Date     string `json:"date"`
			IsOffDay *bool  `json:"isOffDay"`
		} `json:"days"`
	}
	if err := common.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if data.Year != year || len(data.Days) == 0 || len(data.Papers) == 0 {
		return nil, fmt.Errorf("incomplete Chinese holiday calendar for %d", year)
	}
	for _, paper := range data.Papers {
		u, err := url.Parse(paper)
		if err != nil || u.Scheme != "https" || u.Hostname() != "www.gov.cn" {
			return nil, fmt.Errorf("calendar is missing an official State Council source")
		}
	}
	days := map[string]bool{}
	for _, day := range data.Days {
		date, err := time.Parse("2006-01-02", day.Date)
		if err != nil || date.Year() != year || day.IsOffDay == nil {
			return nil, fmt.Errorf("invalid holiday calendar date")
		}
		if _, exists := days[day.Date]; exists {
			return nil, fmt.Errorf("duplicate holiday calendar date")
		}
		days[day.Date] = *day.IsOffDay
	}
	return days, nil
}

func fetchCNHolidayCalendar(ctx context.Context, year int) ([]byte, error) {
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for _, base := range []string{"https://cdn.jsdelivr.net/gh/NateScarlet/holiday-cn@master/", "https://raw.githubusercontent.com/NateScarlet/holiday-cn/master/"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+strconv.Itoa(year)+".json", nil)
		if err != nil {
			return nil, err
		}
		res, err := client.Do(req)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		res.Body.Close()
		if readErr != nil || res.StatusCode != http.StatusOK || len(body) > 1<<20 {
			continue
		}
		if _, err := parseCNHolidayCalendar(body, year); err == nil {
			return body, nil
		}
	}
	return nil, fmt.Errorf("Chinese holiday calendar for %d is unavailable; refusing to guess peak pricing", year)
}

func (cache *cnHolidayCache) year(year int, force bool) (map[string]bool, error) {
	cache.fetchMu.Lock()
	defer cache.fetchMu.Unlock()
	cache.mu.RLock()
	days := cache.years[year]
	last := cache.lastAttempt[year]
	cache.mu.RUnlock()
	if !force {
		if days != nil {
			return days, nil
		}
		if time.Since(last) < time.Hour {
			return nil, fmt.Errorf("Chinese holiday calendar for %d is unavailable", year)
		}
	}
	cache.mu.Lock()
	cache.lastAttempt[year] = time.Now()
	cache.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	body, err := cache.fetch(ctx, year)
	if err != nil {
		return days, err
	}
	updated, err := parseCNHolidayCalendar(body, year)
	if err != nil {
		return days, err
	}
	cache.mu.Lock()
	cache.years[year] = updated
	cache.mu.Unlock()
	return updated, nil
}

func (cache *cnHolidayCache) offPeak(at time.Time) (bool, error) {
	local := at.In(chinaPricingZone)
	// Official DeepSeek weekends stay off-peak even on make-up working days.
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday || local.Hour() < 9 || (local.Hour() >= 12 && local.Hour() < 14) || local.Hour() >= 18 {
		return true, nil
	}
	year := local.Year()
	cache.mu.RLock()
	days := cache.years[year]
	last := cache.lastAttempt[year]
	cache.mu.RUnlock()
	if days == nil {
		var err error
		days, err = cache.year(year, false)
		if err != nil {
			return false, err
		}
	} else if time.Since(last) >= 24*time.Hour {
		// Refresh only while used, and prefetch next year. Existing validated
		// data remains usable during failures; a missing year never guesses.
		cache.mu.Lock()
		start := time.Since(cache.lastAttempt[year]) >= 24*time.Hour
		if start {
			cache.lastAttempt[year] = time.Now()
		}
		cache.mu.Unlock()
		if start {
			go func() { _, _ = cache.year(year, true); _, _ = cache.year(year+1, false) }()
		}
	}
	return days[local.Format("2006-01-02")], nil
}
