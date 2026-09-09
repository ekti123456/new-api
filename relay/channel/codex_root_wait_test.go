package channel

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexRootWaitBudgetIsSharedAndNotRenewed(test *testing.T) {
	for _, source := range []string{"thread_title", "ambient_suggestions", "agent_created_thread", "guardian_review", "memory_consolidation", "subagent", "thread_summary", "thread_description"} {
		test.Run(source, func(test *testing.T) {
			synctest.Test(test, func(test *testing.T) {
				requestContext, _ := gin.CreateTestContext(httptest.NewRecorder())
				requestContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				StartCodexRootAccountWait(requestContext, source)
				time.Sleep(12 * time.Second)
				StartCodexRootAccountWait(requestContext, source)
				remaining := codexRootAccountWaitMillis(requestContext)
				require.NotNil(test, remaining)
				require.EqualValues(test, 18000, *remaining)
				encoded, err := common.Marshal(newAPIPolicyMeta{RootAccountWaitMillis: remaining})
				require.NoError(test, err)
				require.Contains(test, string(encoded), `"root_account_wait_millis":18000`)
				time.Sleep(18 * time.Second)
				remaining = codexRootAccountWaitMillis(requestContext)
				require.NotNil(test, remaining)
				require.Zero(test, *remaining)
				encoded, err = common.Marshal(newAPIPolicyMeta{RootAccountWaitMillis: remaining})
				require.NoError(test, err)
				require.Contains(test, string(encoded), `"root_account_wait_millis":0`)
				_, responseHasDeadline := requestContext.Request.Context().Deadline()
				require.False(test, responseHasDeadline, "root wait must not impose a generation timeout")
			})
		})
	}
}

func TestCodexRootWaitDoesNotAffectUserRequests(test *testing.T) {
	for _, source := range []string{"", "user"} {
		requestContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		StartCodexRootAccountWait(requestContext, source)
		require.Nil(test, codexRootAccountWaitMillis(requestContext), source)
	}
}
