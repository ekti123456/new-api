package common

import "sync"

type CodexBillingReport struct {
	ServiceTier             string `json:"service_tier"`
	Source                  string `json:"source"`
	RequestedServiceTier    string `json:"requested_service_tier,omitempty"`
	ActualServiceTier       string `json:"actual_service_tier,omitempty"`
	LocalBillingServiceTier string `json:"local_billing_service_tier,omitempty"`
	Protocol                string `json:"protocol"`
}

type CodexBillingObservation struct {
	mu     sync.Mutex
	report *CodexBillingReport
}

func (o *CodexBillingObservation) Record(report CodexBillingReport) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.report = &report
}

func (o *CodexBillingObservation) Snapshot() *CodexBillingReport {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.report == nil {
		return nil
	}
	copy := *o.report
	return &copy
}
