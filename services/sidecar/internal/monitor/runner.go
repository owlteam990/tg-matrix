package monitor

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/tg-matrix/sidecar/internal/store"
	"github.com/tg-matrix/sidecar/internal/tgclient"
)

const (
	defaultTick       = 4 * time.Second
	perGroupTimeout   = 45 * time.Second
	perGroupFetchLimit = 50
	betweenGroupsPause = 400 * time.Millisecond
)

// Runner 群监控后台轮询：对 status=monitoring 的群拉新消息入库。
type Runner struct {
	Store  *store.Store
	TG     *tgclient.Manager
	tick   time.Duration
	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	busy   bool
}

/**
 * 创建群监控 Runner。
 *
 * @param st 存储
 * @param tg Telegram 管理器
 */
func NewRunner(st *store.Store, tg *tgclient.Manager) *Runner {
	return &Runner{
		Store:  st,
		TG:     tg,
		tick:   defaultTick,
		stopCh: make(chan struct{}),
	}
}

/**
 * 启动后台 tick。
 */
func (r *Runner) Start() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(r.tick)
		defer t.Stop()
		for {
			select {
			case <-r.stopCh:
				return
			case <-t.C:
				r.safeTick()
			}
		}
	}()
	log.Printf("group monitor started (tick=%s)", r.tick)
}

/**
 * 停止并等待退出。
 */
func (r *Runner) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

func (r *Runner) safeTick() {
	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		return
	}
	r.busy = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.busy = false
		r.mu.Unlock()
	}()
	if err := r.tickOnce(); err != nil {
		log.Printf("group monitor: %v", err)
	}
}

func (r *Runner) tickOnce() error {
	if r.Store == nil || r.TG == nil || !r.TG.Configured() {
		return nil
	}
	items, err := r.Store.ListGroupMonitorsByStatus(store.GroupMonitorMonitoring)
	if err != nil {
		return err
	}
	for _, m := range items {
		select {
		case <-r.stopCh:
			return nil
		default:
		}
		r.pollOne(m)
		time.Sleep(betweenGroupsPause)
	}
	return nil
}

func (r *Runner) pollOne(m store.GroupMonitor) {
	ctx, cancel := context.WithTimeout(context.Background(), perGroupTimeout)
	defer cancel()
	msgs, err := r.TG.FetchGroupHistorySince(
		ctx, m.AccountID, m.PeerType, m.PeerID, m.AccessHash, m.LastMsgID, perGroupFetchLimit,
	)
	if err != nil {
		_ = r.Store.SetGroupMonitorStatus(m.AccountID, m.PeerID, store.GroupMonitorMonitoring, err.Error())
		log.Printf("group monitor %s/%d: %v", m.AccountID, m.PeerID, err)
		return
	}
	if len(msgs) == 0 {
		return
	}
	rows := make([]store.GroupMessage, 0, len(msgs))
	for _, msg := range msgs {
		rows = append(rows, store.GroupMessage{
			AccountID: m.AccountID,
			PeerID:    m.PeerID,
			MsgID:     msg.ID,
			Date:      msg.Date,
			FromID:    msg.FromID,
			FromName:  msg.FromName,
			Out:       msg.Out,
			Text:      msg.Text,
			Media:     tgclient.FormatChatMediaLabel(msg.Media),
		})
	}
	if _, err := r.Store.InsertGroupMessagesIgnoreDup(rows); err != nil {
		log.Printf("group monitor insert %s/%d: %v", m.AccountID, m.PeerID, err)
		return
	}
	_ = r.Store.RefreshGroupMonitorCounts(m.AccountID, m.PeerID)
	_ = r.Store.SetGroupMonitorStatus(m.AccountID, m.PeerID, store.GroupMonitorMonitoring, "")
}
