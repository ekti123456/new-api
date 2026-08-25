package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetEnabledKeyAtPinsExactMultiKey(t *testing.T) {
	channel := &Channel{
		Id:   99101,
		Key:  "key-zero\nkey-one\nkey-two",
		Keys: []string{"key-zero", "key-one", "key-two"},
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled},
		},
	}

	key, err := channel.GetEnabledKeyAt(2)
	require.Nil(t, err)
	require.Equal(t, "key-two", key)

	_, err = channel.GetEnabledKeyAt(1)
	require.NotNil(t, err)
	_, err = channel.GetEnabledKeyAt(3)
	require.NotNil(t, err)
}

func TestGetEnabledKeyAtValidatesSingleKeyIndex(t *testing.T) {
	channel := &Channel{Id: 99102, Key: "single-key"}
	key, err := channel.GetEnabledKeyAt(0)
	require.Nil(t, err)
	require.Equal(t, "single-key", key)
	_, err = channel.GetEnabledKeyAt(1)
	require.NotNil(t, err)
}
