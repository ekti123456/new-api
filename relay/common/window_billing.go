package common

import "time"

type WindowBillingGrant struct {
	Ticket      string    `json:"-"`
	BindingHash string    `json:"-"`
	ID          string    `json:"id"`
	Root        string    `json:"root"`
	Fingerprint string    `json:"-"`
	ExpiresAt   time.Time `json:"expires_at"`
	Expanded    bool      `json:"expanded"`
	Multiplier  float64   `json:"multiplier"`
}

func (info *RelayInfo) WindowMultiplier() float64 {
	if info == nil || info.WindowBilling == nil || !info.WindowBilling.Expanded {
		return 1
	}
	return info.WindowBilling.Multiplier
}
