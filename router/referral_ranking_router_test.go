package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminReferralRankingRouteRejectsUnauthenticatedRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	request := httptest.NewRequest(http.MethodGet, "/api/data/referral-rankings?period=all", nil)
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Contains(t, response.Body.String(), "AUTH_UNAUTHORIZED")
}

func TestAdminReferralRankingRouteRequiresAdminAndAllowsAdmin(t *testing.T) {
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.SubscriptionOrder{},
		&model.ReferralCommission{},
	))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		sqlDB, databaseErr := db.DB()
		if databaseErr == nil {
			_ = sqlDB.Close()
		}
	})

	createUser := func(username, token string, role int) {
		t.Helper()
		user := model.User{
			Username: username, Password: "password-placeholder", AffCode: "aff-" + username,
			AccessToken: &token, Status: common.UserStatusEnabled, Role: role,
			Group: "default", AuthVersion: 1,
		}
		require.NoError(t, db.Create(&user).Error)
	}
	createUser("ranking-common", "ranking-common-token", common.RoleCommonUser)
	createUser("ranking-admin", "ranking-admin-token", common.RoleAdminUser)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	ordinaryRequest := httptest.NewRequest(http.MethodGet, "/api/data/referral-rankings?period=all", nil)
	ordinaryRequest.Header.Set("Authorization", "Bearer ranking-common-token")
	ordinaryResponse := httptest.NewRecorder()
	engine.ServeHTTP(ordinaryResponse, ordinaryRequest)
	assert.Equal(t, http.StatusForbidden, ordinaryResponse.Code)
	assert.Contains(t, ordinaryResponse.Body.String(), "AUTH_INSUFFICIENT_PRIVILEGE")

	adminRequest := httptest.NewRequest(http.MethodGet, "/api/data/referral-rankings?period=all", nil)
	adminRequest.Header.Set("Authorization", "Bearer ranking-admin-token")
	adminResponse := httptest.NewRecorder()
	engine.ServeHTTP(adminResponse, adminRequest)
	assert.Equal(t, http.StatusOK, adminResponse.Code)
	assert.Contains(t, adminResponse.Body.String(), `"success":true`)
}
