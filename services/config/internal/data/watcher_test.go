package data

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestWatcher 造一个不连数据库、但已处于「监听正常」状态的 Watcher。
// 扇出逻辑与 LISTEN 连接是解耦的,这里要测的就是前者。
func newTestWatcher() *Watcher {
	w := &Watcher{
		log:  zap.NewNop(),
		subs: make(map[*subscriber]struct{}),
	}
	w.setHealthy()
	return w
}

func recvWithin(t *testing.T, ch <-chan biz.ChangeEvent) (biz.ChangeEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an event")
		return biz.ChangeEvent{}, false
	}
}

func TestSubscribe_FiltersByNamespaceEnvAndKey(t *testing.T) {
	w := newTestWatcher()

	// 点名订阅一个 key
	scoped, cancelScoped := w.Subscribe("cart", "dev", []string{"bootstrap.yaml"})
	defer cancelScoped()
	// keys 为空 = 订阅该 ns/env 下全部 key
	all, cancelAll := w.Subscribe("cart", "dev", nil)
	defer cancelAll()
	// 另一个环境,不该收到任何 cart/dev 的事件
	other, cancelOther := w.Subscribe("cart", "prod", nil)
	defer cancelOther()

	w.publish(biz.ChangeEvent{Namespace: "cart", Environment: "dev", Key: "other.yaml"})
	w.publish(biz.ChangeEvent{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml", Version: 7})

	// 点名订阅者只该看到自己那个 key
	ev, ok := recvWithin(t, scoped)
	require.True(t, ok)
	assert.Equal(t, "bootstrap.yaml", ev.Key)
	assert.Equal(t, int32(7), ev.Version)
	assert.Empty(t, scoped, "不该收到没订阅的 key")

	// 全量订阅者两条都收到
	first, ok := recvWithin(t, all)
	require.True(t, ok)
	assert.Equal(t, "other.yaml", first.Key)
	second, ok := recvWithin(t, all)
	require.True(t, ok)
	assert.Equal(t, "bootstrap.yaml", second.Key)

	assert.Empty(t, other, "别的 environment 不该收到事件")
}

// 慢订阅者不能拖住监听协程:缓冲满了就丢事件,publish 必须始终立即返回。
func TestPublish_DropsWhenSubscriberBufferIsFull(t *testing.T) {
	w := newTestWatcher()
	ch, cancel := w.Subscribe("cart", "dev", nil)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subBuffer*3; i++ {
			w.publish(biz.ChangeEvent{Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml"})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a full subscriber buffer")
	}

	assert.Len(t, ch, subBuffer, "缓冲应被填满,多出来的事件直接丢弃")
}

// 链路断开必须让每个订阅者都感知到 —— 关闭 channel 而不是静默停止投递。
func TestFailAll_ClosesEverySubscriber(t *testing.T) {
	w := newTestWatcher()
	a, _ := w.Subscribe("cart", "dev", nil)
	b, _ := w.Subscribe("order", "prod", []string{"bootstrap.yaml"})

	w.failAll()

	_, ok := recvWithin(t, a)
	assert.False(t, ok, "订阅 channel 应被关闭")
	_, ok = recvWithin(t, b)
	assert.False(t, ok, "订阅 channel 应被关闭")

	// 断链期间新来的订阅同样立刻拿到已关闭的 channel,而不是一条永远静默的流
	c, cancel := w.Subscribe("cart", "dev", nil)
	defer cancel()
	_, ok = recvWithin(t, c)
	assert.False(t, ok, "链路不健康时新订阅应直接返回已关闭的 channel")
}

// cancel 幂等:重复调用不能 panic(close 已关闭的 channel 会 panic)。
func TestSubscribe_CancelIsIdempotent(t *testing.T) {
	w := newTestWatcher()
	ch, cancel := w.Subscribe("cart", "dev", nil)

	cancel()
	assert.NotPanics(t, cancel)

	_, ok := recvWithin(t, ch)
	assert.False(t, ok)
}

// cancel 与 failAll 竞争同一个订阅者时不能重复 close。
func TestFailAllThenCancel_DoesNotPanic(t *testing.T) {
	w := newTestWatcher()
	_, cancel := w.Subscribe("cart", "dev", nil)

	w.failAll()
	assert.NotPanics(t, cancel)
}

// payload 只带定位信息:value 不能出现在通知通道里(8000 字节上限 + 密钥不该多走一条路径)。
func TestChangePayload_CarriesOnlyLocators(t *testing.T) {
	b, err := json.Marshal(changePayload{
		Namespace: "cart", Environment: "dev", Key: "bootstrap.yaml", Version: 3,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ns":"cart","env":"dev","key":"bootstrap.yaml","ver":3}`, string(b))

	var p changePayload
	require.NoError(t, json.Unmarshal([]byte(`{"ns":"a","env":"b","key":"c","ver":9,"del":true}`), &p))
	assert.Equal(t, changePayload{Namespace: "a", Environment: "b", Key: "c", Version: 9, Deleted: true}, p)
}
