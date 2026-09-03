package model

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestClaimDailyQuotaNotificationUsesNaturalDayAndUserScope(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{
		Id:       4101,
		Username: "quota-notify-1",
		AffCode:  "quota-notify-aff-1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Id:       4102,
		Username: "quota-notify-2",
		AffCode:  "quota-notify-aff-2",
		Status:   common.UserStatusEnabled,
	}).Error)

	location := time.FixedZone("UTC+8", 8*60*60)
	first := time.Date(2026, time.September, 3, 23, 59, 0, 0, location)

	claimed, err := ClaimDailyQuotaNotification(4101, first)
	require.NoError(t, err)
	require.True(t, claimed)

	claimed, err = ClaimDailyQuotaNotification(4101, first.Add(30*time.Second))
	require.NoError(t, err)
	require.False(t, claimed)

	claimed, err = ClaimDailyQuotaNotification(4102, first.Add(30*time.Second))
	require.NoError(t, err)
	require.True(t, claimed)

	claimed, err = ClaimDailyQuotaNotification(4101, first.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestClaimDailyQuotaNotificationIsAtomicUnderConcurrency(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{
		Id:       4201,
		Username: "quota-notify-concurrent",
		AffCode:  "quota-notify-aff-concurrent",
		Status:   common.UserStatusEnabled,
	}).Error)

	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	const callers = 32
	var claimedCount atomic.Int32
	errors := make(chan error, callers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer waitGroup.Done()
			claimed, err := ClaimDailyQuotaNotification(4201, now)
			if err != nil {
				errors <- err
				return
			}
			if claimed {
				claimedCount.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), claimedCount.Load())
}

func TestUserUpdateDoesNotOverwriteQuotaNotificationClaim(t *testing.T) {
	truncateTables(t)
	user := User{
		Id:       4301,
		Username: "quota-notify-stale-update",
		AffCode:  "quota-notify-aff-stale-update",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&user).Error)

	location := time.FixedZone("UTC+8", 8*60*60)
	firstDay := time.Date(2026, time.September, 3, 12, 0, 0, 0, location)
	claimed, err := ClaimDailyQuotaNotification(user.Id, firstDay)
	require.NoError(t, err)
	require.True(t, claimed)

	var staleUser User
	require.NoError(t, DB.First(&staleUser, user.Id).Error)
	secondDay := firstDay.AddDate(0, 0, 1)
	claimed, err = ClaimDailyQuotaNotification(user.Id, secondDay)
	require.NoError(t, err)
	require.True(t, claimed)

	staleUser.DisplayName = "updated"
	require.NoError(t, staleUser.Update(false))

	claimed, err = ClaimDailyQuotaNotification(user.Id, secondDay.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, claimed)
}
