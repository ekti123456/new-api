package model

import (
	"context"
	"errors"
)

func CreateRelayAdmissionError(ctx context.Context, performance *PerfMetricError, usage *Log) error {
	var failures []error
	if performance != nil {
		if DB == nil {
			failures = append(failures, errors.New("admission audit database unavailable"))
		} else if err := DB.WithContext(ctx).Create(performance).Error; err != nil {
			failures = append(failures, err)
		}
	}
	if usage != nil {
		ensureLogRequestId(usage)
		if LOG_DB == nil {
			failures = append(failures, errors.New("admission error log database unavailable"))
		} else if err := LOG_DB.WithContext(ctx).Create(usage).Error; err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
