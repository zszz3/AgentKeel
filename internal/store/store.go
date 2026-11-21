// Package store 存事件日志。日志只追加，不改不删。
package store

import (
	"context"
	"errors"

	"github.com/zszz3/agentkeel/internal/event"
)

// ErrConflict 表示要写的 seq 不是这个会话的下一个序号：
// 要么已经有人写过这个位置，要么中间空了位置。
// 两个进程同时跑同一个会话时，后写的那个会拿到这个错误。
var ErrConflict = errors.New("store: seq conflict")

type Store interface {
	// Append 写入一条事件。e.Seq 必须等于该会话当前最大 seq + 1（新会话从 1 开始），
	// 否则返回 ErrConflict。返回 nil 时事件已经落盘。
	Append(ctx context.Context, e event.Event) error

	// Load 按 seq 从小到大返回一个会话的全部事件。会话不存在时返回空切片。
	Load(ctx context.Context, sessionID string) ([]event.Event, error)

	// Sessions 返回所有会话 ID，最近创建的在前。
	Sessions(ctx context.Context) ([]string, error)

	Close() error
}
