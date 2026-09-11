package setting

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

var backgroundUserConcurrencyLimit int64 = 5

func GetBackgroundUserConcurrencyLimit() int {
	return int(atomic.LoadInt64(&backgroundUserConcurrencyLimit))
}

func ParseBackgroundUserConcurrencyLimit(value string) (int, error) {
	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || limit < 1 || limit > 100000 {
		return 0, fmt.Errorf("background user concurrency limit must be between 1 and 100000")
	}
	return limit, nil
}

func UpdateBackgroundUserConcurrencyLimit(value string) error {
	limit, err := ParseBackgroundUserConcurrencyLimit(value)
	if err != nil {
		return err
	}
	atomic.StoreInt64(&backgroundUserConcurrencyLimit, int64(limit))
	return nil
}
