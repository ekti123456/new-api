package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func resetLocalUserConcurrencyForTest() {
	localUserConcurrency.mu.Lock()
	localUserConcurrency.active = make(map[int]map[string]struct{})
	localUserConcurrency.mu.Unlock()
}

func TestEffectiveUserConcurrencyLimit(t *testing.T) {
	previousDefault := setting.DefaultUserConcurrencyLimit
	setting.DefaultUserConcurrencyLimit = 5
	t.Cleanup(func() { setting.DefaultUserConcurrencyLimit = previousDefault })

	limit, source := EffectiveUserConcurrencyLimit(-1)
	require.Equal(t, 5, limit)
	require.Equal(t, "default", source)

	limit, source = EffectiveUserConcurrencyLimit(0)
	require.Zero(t, limit)
	require.Equal(t, "unlimited", source)

	limit, source = EffectiveUserConcurrencyLimit(9)
	require.Equal(t, 9, limit)
	require.Equal(t, "user", source)
}

func TestLocalUserConcurrencyAcquireRelease(t *testing.T) {
	resetLocalUserConcurrencyForTest()
	t.Cleanup(resetLocalUserConcurrencyForTest)

	require.True(t, acquireLocalUserConcurrency(7, 2, "req-1"))
	require.True(t, acquireLocalUserConcurrency(7, 2, "req-2"))
	require.False(t, acquireLocalUserConcurrency(7, 2, "req-3"))
	require.Equal(t, 2, getLocalUserConcurrency(7))
	require.Equal(t, 2, getLocalTotalConcurrency())
	require.True(t, acquireLocalUserConcurrency(9, 0, "req-unlimited"))
	require.Equal(t, 3, getLocalTotalConcurrency())

	releaseLocalUserConcurrency(7, "req-1")
	require.True(t, acquireLocalUserConcurrency(7, 2, "req-3"))
	require.Equal(t, 2, getLocalUserConcurrency(7))
	require.Equal(t, 3, getLocalTotalConcurrency())
}

func TestRedisUserConcurrencyAcquireRelease(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	previousRedis := common.RedisEnabled
	previousClient := common.RDB
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
		common.RDB = previousClient
		_ = client.Close()
	})

	ctx := t.Context()
	acquired, err := acquireUserConcurrency(ctx, 8, 2, "req-1")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = acquireUserConcurrency(ctx, 8, 2, "req-2")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = acquireUserConcurrency(ctx, 8, 2, "req-3")
	require.NoError(t, err)
	require.False(t, acquired)

	current, err := GetUserCurrentConcurrency(ctx, 8)
	require.NoError(t, err)
	require.Equal(t, 2, current)
	total, err := GetTotalCurrentConcurrency(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, total)

	releaseUserConcurrency(8, "req-1")
	total, err = GetTotalCurrentConcurrency(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	acquired, err = acquireUserConcurrency(ctx, 8, 2, "req-3")
	require.NoError(t, err)
	require.True(t, acquired)
	total, err = GetTotalCurrentConcurrency(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, total)
}

func TestModelRequestConcurrencyTracksWhenLimitDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousRedis := common.RedisEnabled
	previousEnabled := setting.ModelRequestConcurrencyLimitEnabled
	common.RedisEnabled = false
	setting.ModelRequestConcurrencyLimitEnabled = false
	resetLocalUserConcurrencyForTest()
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
		setting.ModelRequestConcurrencyLimitEnabled = previousEnabled
		resetLocalUserConcurrencyForTest()
	})

	started := make(chan struct{})
	release := make(chan struct{})
	router := gin.New()
	router.Use(RequestId())
	router.Use(func(c *gin.Context) {
		c.Set("id", 43)
		common.SetContextKey(c, constant.ContextKeyUserConcurrencyLimit, -1)
		c.Next()
	})
	router.Use(ModelRequestConcurrencyLimit())
	router.GET("/tracked", func(c *gin.Context) {
		close(started)
		<-release
		c.Status(http.StatusOK)
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/tracked", nil))
		done <- recorder
	}()
	<-started
	require.Equal(t, 1, getLocalTotalConcurrency())
	close(release)
	require.Equal(t, http.StatusOK, (<-done).Code)
	require.Zero(t, getLocalTotalConcurrency())
}

func TestModelRequestConcurrencyLimitBlocksUntilSlotReleased(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousRedis := common.RedisEnabled
	previousEnabled := setting.ModelRequestConcurrencyLimitEnabled
	previousDefault := setting.DefaultUserConcurrencyLimit
	common.RedisEnabled = false
	setting.ModelRequestConcurrencyLimitEnabled = true
	setting.DefaultUserConcurrencyLimit = 1
	resetLocalUserConcurrencyForTest()
	t.Cleanup(func() {
		common.RedisEnabled = previousRedis
		setting.ModelRequestConcurrencyLimitEnabled = previousEnabled
		setting.DefaultUserConcurrencyLimit = previousDefault
		resetLocalUserConcurrencyForTest()
	})

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	router := gin.New()
	router.Use(RequestId())
	router.Use(func(c *gin.Context) {
		c.Set("id", 42)
		common.SetContextKey(c, constant.ContextKeyUserConcurrencyLimit, -1)
		c.Next()
	})
	router.Use(ModelRequestConcurrencyLimit())
	router.GET("/limited", func(c *gin.Context) {
		once.Do(func() { close(started) })
		<-release
		c.Status(http.StatusOK)
	})

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/limited", nil))
		firstDone <- recorder
	}()
	<-started

	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/limited", nil))
	require.Equal(t, http.StatusTooManyRequests, blocked.Code)
	require.Contains(t, blocked.Body.String(), "user_concurrency_limit_exceeded")

	close(release)
	first := <-firstDone
	require.Equal(t, http.StatusOK, first.Code)
	require.Zero(t, getLocalUserConcurrency(42))
}
