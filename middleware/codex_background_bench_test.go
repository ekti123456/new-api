package middleware

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func BenchmarkCodexBackgroundChannelSelection(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		for _, explicit := range []bool{false, true} {
			b.Run(fmt.Sprintf("channels_%d/explicit_%t", count, explicit), func(b *testing.B) {
				ch, _, _ := setupCodexRootDistributorTest(b)
				unboundURL := "https://unbound.example"
				others := make([]model.Channel, 0, count-1)
				for index := 1; index < count; index++ {
					others = append(others, model.Channel{Id: ch.Id + index, Type: ch.Type, Key: "unbound-key", BaseURL: &unboundURL, Group: "pro", Models: "gpt-5.6-sol", Status: common.ChannelStatusEnabled})
				}
				if len(others) > 0 {
					require.NoError(b, model.DB.CreateInBatches(&others, 50).Error)
					model.InitChannelCache()
				}
				c, _ := codexUnlinkedNativeTitleContext(997001, 997002, "01a17841-0000-7000-8000-000000000901")
				if explicit {
					common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(ch.Id))
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					selected, _, _, _, found, err := loadCodexBackgroundChannel(c, "pro", "gpt-5.6-luna")
					if err != nil || !found || selected.Id != ch.Id {
						b.Fatal("background destination unavailable")
					}
				}
			})
		}
	}
}
