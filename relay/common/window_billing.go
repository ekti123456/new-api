package common

import "time"

type WindowBillingGrant struct {
	Ticket        string    `json:"-"`
	BindingHash   string    `json:"-"`
	ID            string    `json:"id"`
	Root          string    `json:"root"`
	Fingerprint   string    `json:"-"`
	ExpiresAt     time.Time `json:"expires_at"`
	PendingUntil  time.Time `json:"pending_until,omitempty"`
	Confirmed     bool      `json:"confirmed"`
	NoWindow      bool      `json:"no_window,omitempty"`
	ReservationID string    `json:"-"`
	Expanded      bool      `json:"expanded"`
	Multiplier    float64   `json:"multiplier"`
}

func (info *RelayInfo) WindowMultiplier() float64 {
	if info == nil || info.WindowBilling == nil || !info.WindowBilling.Expanded {
		return 1
	}
	return info.WindowBilling.Multiplier
}
