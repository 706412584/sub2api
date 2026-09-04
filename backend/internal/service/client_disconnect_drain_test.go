//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 客户端断开后的 drain 必须有界：drain 只为补全 usage 计费，
// 超过上限后按已收到的 usage 返回并释放并发槽。
// 线上事故：断流重试风暴下每个僵尸响应占槽 3-11 分钟（实测 688s），
// 用户并发槽被打满后所有请求 429。
func TestClientDisconnectDrainTimeoutBounded(t *testing.T) {
	require.Equal(t, 30*time.Second, clientDisconnectDrainTimeout,
		"drain 上限应远小于 stream interval 默认 180s")
	require.Less(t, clientDisconnectDrainTimeout, 180*time.Second,
		"drain 上限必须小于 stream data interval 默认值，否则修复无效")
}
