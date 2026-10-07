package common

const GatewayInstanceIDOption = "GatewayInstanceID"

// This is server-owned state, not a value supplied in client headers or body.
func GatewayInstanceID() string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	return OptionMap[GatewayInstanceIDOption]
}
