package rpc

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"anubisbot/internal/config"
	"anubisbot/internal/timesync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

var rpcIDSanitize = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func normalizeRPCURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid url")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss":
	default:
		return "", fmt.Errorf("url scheme must be http/https/ws/wss")
	}
	return raw, nil
}

func suggestRPCID(rawURL, name string) string {
	if id := rpcIDSanitize.ReplaceAllString(strings.TrimSpace(name), "-"); id != "" {
		return strings.Trim(id, "-")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "rpc"
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	id := rpcIDSanitize.ReplaceAllString(host, "-")
	id = strings.Trim(id, "-")
	if id == "" {
		return "rpc"
	}
	return id
}

func uniqueRPCID(base string, existing []config.RPCEndpoint) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "rpc"
	}
	used := map[string]bool{}
	for _, r := range existing {
		used[r.ID] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !used[cand] {
			return cand
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()%100000)
}

type Manager struct {
	mu        sync.RWMutex
	cfg       *config.ChainConfig
	path      string
	clients   map[string]*ethclient.Client
	activeIDs []string
	syncer    *timesync.Sync
	rtts      map[string]int64
	failUntil map[string]time.Time
	failCount map[string]int
	pingIdx   int
}

func New(cfg *config.ChainConfig, path string, syncer *timesync.Sync) (*Manager, error) {
	m := NewSoft(cfg, path, syncer)
	if err := m.ensureClient(m.PrimaryID()); err != nil {
		return nil, err
	}
	return m, nil
}

// NewSoft creates a manager without requiring an immediate successful dial.
func NewSoft(cfg *config.ChainConfig, path string, syncer *timesync.Sync) *Manager {
	ids := normalizeActiveIDs(cfg)
	m := &Manager{
		cfg:       cfg,
		path:      path,
		clients:   map[string]*ethclient.Client{},
		activeIDs: ids,
		syncer:    syncer,
		rtts:      map[string]int64{},
		failUntil: map[string]time.Time{},
		failCount: map[string]int{},
	}
	cfg.ActiveRpcIDs = append([]string{}, ids...)
	if len(ids) > 0 {
		cfg.ActiveRpcID = ids[0]
	}
	return m
}

func normalizeActiveIDs(cfg *config.ChainConfig) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		for _, r := range cfg.RPCs {
			if r.ID == id {
				seen[id] = true
				ids = append(ids, id)
				return
			}
		}
	}
	for _, id := range cfg.ActiveRpcIDs {
		add(id)
	}
	if len(ids) == 0 && cfg.ActiveRpcID != "" {
		add(cfg.ActiveRpcID)
	}
	if len(ids) == 0 && len(cfg.RPCs) > 0 {
		ids = []string{cfg.RPCs[0].ID}
	}
	return ids
}

func (m *Manager) ensureClient(id string) error {
	m.mu.RLock()
	if _, ok := m.clients[id]; ok {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	ep, err := m.endpoint(id)
	if err != nil {
		return err
	}
	cli, err := ethclient.Dial(ep.URL)
	if err != nil {
		return fmt.Errorf("dial %s: %w", id, err)
	}
	m.mu.Lock()
	m.clients[id] = cli
	m.mu.Unlock()
	return nil
}

func (m *Manager) endpoint(id string) (config.RPCEndpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.cfg.RPCs {
		if r.ID == id {
			return r, nil
		}
	}
	return config.RPCEndpoint{}, fmt.Errorf("rpc not found: %s", id)
}

func (m *Manager) Active() config.RPCEndpoint {
	return m.Primary()
}

func (m *Manager) Primary() config.RPCEndpoint {
	ep, _ := m.endpoint(m.PrimaryID())
	return ep
}

func (m *Manager) PrimaryID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.activeIDs) == 0 {
		return ""
	}
	return m.activeIDs[0]
}

func (m *Manager) ActiveID() string {
	return m.PrimaryID()
}

func (m *Manager) ActiveIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.activeIDs))
	copy(out, m.activeIDs)
	return out
}

func (m *Manager) Client() (*ethclient.Client, error) {
	return m.ClientByID(m.PrimaryID())
}

func (m *Manager) ClientByID(id string) (*ethclient.Client, error) {
	if id == "" {
		return nil, fmt.Errorf("empty rpc id")
	}
	if err := m.ensureClient(id); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[id], nil
}

func (m *Manager) SendTransaction(ctx context.Context, id string, tx *types.Transaction) error {
	cli, err := m.ClientByID(id)
	if err != nil {
		return err
	}
	return cli.SendTransaction(ctx, tx)
}

// WaitReceipt polls until the tx is mined or ctx/timeout.
func (m *Manager) WaitReceipt(ctx context.Context, rpcID, txHash string, timeout time.Duration) (*types.Receipt, error) {
	cli, err := m.ClientByID(rpcID)
	if err != nil {
		cli, err = m.Client()
		if err != nil {
			return nil, err
		}
	}
	hash := common.HexToHash(txHash)
	deadline := time.Now().Add(timeout)
	for {
		rec, err := cli.TransactionReceipt(ctx, hash)
		if err == nil && rec != nil {
			return rec, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, err
			}
			return nil, context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func (m *Manager) List() []config.RPCEndpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.RPCEndpoint, len(m.cfg.RPCs))
	copy(out, m.cfg.RPCs)
	return out
}

func (m *Manager) RTTs() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]int64, len(m.rtts))
	for k, v := range m.rtts {
		out[k] = v
	}
	return out
}

// Leads returns half-RTT lead ms for each active RPC (0 if no rtt / backoff).
func (m *Manager) Leads() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]int64, len(m.activeIDs))
	now := time.Now()
	for _, id := range m.activeIDs {
		if until, ok := m.failUntil[id]; ok && now.Before(until) {
			out[id] = 0
			continue
		}
		rtt := m.rtts[id]
		if rtt <= 0 {
			out[id] = 0
			continue
		}
		out[id] = rtt / 2
	}
	return out
}

func (m *Manager) MaxLeadMs() int64 {
	leads := m.Leads()
	var max int64
	for _, v := range leads {
		if v > max {
			max = v
		}
	}
	return max
}

func (m *Manager) SetActive(id string) error {
	return m.SetActiveIDs([]string{id})
}

func (m *Manager) SetActiveIDs(ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("at least one rpc required")
	}
	seen := map[string]bool{}
	var clean []string
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		if _, err := m.endpoint(id); err != nil {
			return err
		}
		seen[id] = true
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return fmt.Errorf("at least one rpc required")
	}
	if err := m.ensureClient(clean[0]); err != nil {
		return err
	}
	m.mu.Lock()
	m.activeIDs = clean
	m.cfg.ActiveRpcIDs = append([]string{}, clean...)
	m.cfg.ActiveRpcID = clean[0]
	m.mu.Unlock()
	return config.SaveChain(m.path, m.cfg)
}

func (m *Manager) Add(ep config.RPCEndpoint) (config.RPCEndpoint, error) {
	normalized, err := normalizeRPCURL(ep.URL)
	if err != nil {
		return ep, err
	}
	ep.URL = normalized
	ep.ID = strings.TrimSpace(ep.ID)
	ep.Name = strings.TrimSpace(ep.Name)
	m.mu.Lock()
	if ep.ID == "" {
		ep.ID = uniqueRPCID(suggestRPCID(ep.URL, ep.Name), m.cfg.RPCs)
	} else {
		for _, r := range m.cfg.RPCs {
			if r.ID == ep.ID {
				m.mu.Unlock()
				return ep, fmt.Errorf("rpc id exists")
			}
		}
	}
	if ep.Name == "" {
		ep.Name = ep.ID
	}
	ep.Builtin = false
	m.cfg.RPCs = append(m.cfg.RPCs, ep)
	m.mu.Unlock()
	if err := config.SaveChain(m.path, m.cfg); err != nil {
		return ep, err
	}
	return ep, nil
}

func (m *Manager) Update(id, name, rawURL string) error {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" {
		return fmt.Errorf("id required")
	}
	normalized, err := normalizeRPCURL(rawURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i, r := range m.cfg.RPCs {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("rpc not found")
	}
	ep := m.cfg.RPCs[idx]
	if name == "" {
		name = ep.Name
	}
	if name == "" {
		name = id
	}
	ep.Name = name
	ep.URL = normalized
	m.cfg.RPCs[idx] = ep
	delete(m.clients, id) // force re-dial with new URL
	return config.SaveChain(m.path, m.cfg)
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i, r := range m.cfg.RPCs {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("rpc not found")
	}
	m.cfg.RPCs = append(m.cfg.RPCs[:idx], m.cfg.RPCs[idx+1:]...)
	var next []string
	for _, a := range m.activeIDs {
		if a != id {
			next = append(next, a)
		}
	}
	if len(next) == 0 && len(m.cfg.RPCs) > 0 {
		next = []string{m.cfg.RPCs[0].ID}
	}
	m.activeIDs = next
	m.cfg.ActiveRpcIDs = append([]string{}, next...)
	if len(next) > 0 {
		m.cfg.ActiveRpcID = next[0]
	} else {
		m.cfg.ActiveRpcID = ""
	}
	delete(m.clients, id)
	delete(m.rtts, id)
	delete(m.failUntil, id)
	delete(m.failCount, id)
	return config.SaveChain(m.path, m.cfg)
}

func (m *Manager) Replace(list []config.RPCEndpoint) ([]config.RPCEndpoint, error) {
	if len(list) == 0 {
		return nil, fmt.Errorf("at least one rpc required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	oldByID := map[string]config.RPCEndpoint{}
	for _, r := range m.cfg.RPCs {
		oldByID[r.ID] = r
	}

	next := make([]config.RPCEndpoint, 0, len(list))
	seen := map[string]bool{}
	for _, raw := range list {
		normalized, err := normalizeRPCURL(raw.URL)
		if err != nil {
			return nil, err
		}
		ep := config.RPCEndpoint{
			ID:   strings.TrimSpace(raw.ID),
			Name: strings.TrimSpace(raw.Name),
			URL:  normalized,
		}
		if ep.ID == "" {
			ep.ID = uniqueRPCID(suggestRPCID(ep.URL, ep.Name), next)
		}
		if seen[ep.ID] {
			return nil, fmt.Errorf("duplicate id: %s", ep.ID)
		}
		seen[ep.ID] = true
		if ep.Name == "" {
			ep.Name = ep.ID
		}
		if old, ok := oldByID[ep.ID]; ok {
			ep.Builtin = old.Builtin
		} else {
			ep.Builtin = false
		}
		next = append(next, ep)
	}

	oldIDs := map[string]bool{}
	for id := range oldByID {
		oldIDs[id] = true
	}
	for _, ep := range next {
		delete(oldIDs, ep.ID)
		if old, ok := oldByID[ep.ID]; ok && old.URL != ep.URL {
			delete(m.clients, ep.ID)
		}
	}
	for id := range oldIDs {
		delete(m.clients, id)
		delete(m.rtts, id)
		delete(m.failUntil, id)
		delete(m.failCount, id)
	}

	m.cfg.RPCs = next
	var active []string
	for _, a := range m.activeIDs {
		if seen[a] {
			active = append(active, a)
		}
	}
	if len(active) == 0 {
		active = []string{next[0].ID}
	}
	m.activeIDs = active
	m.cfg.ActiveRpcIDs = append([]string{}, active...)
	m.cfg.ActiveRpcID = active[0]

	if err := config.SaveChain(m.path, m.cfg); err != nil {
		return nil, err
	}
	out := make([]config.RPCEndpoint, len(next))
	copy(out, next)
	return out, nil
}

// ProbeURL dials url, measures BlockNumber RTT, closes the client. Does not mutate Manager.
func ProbeURL(ctx context.Context, rawURL string) (normalized string, rttMs int64, err error) {
	normalized, err = normalizeRPCURL(rawURL)
	if err != nil {
		return "", 0, err
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
	}
	start := time.Now()
	cli, err := ethclient.DialContext(ctx, normalized)
	if err != nil {
		return normalized, 0, err
	}
	defer cli.Close()
	_, err = cli.BlockNumber(ctx)
	rttMs = time.Since(start).Milliseconds()
	if err != nil {
		return normalized, rttMs, err
	}
	return normalized, rttMs, nil
}

func (m *Manager) PingActive(ctx context.Context) error {
	return m.pingOne(ctx, m.PrimaryID(), true)
}

// pingOne measures RPC RTT; clock offset is maintained separately via NTP (ms).
func (m *Manager) pingOne(ctx context.Context, id string, _ bool) error {
	if id == "" {
		return fmt.Errorf("no rpc")
	}
	m.mu.RLock()
	until := m.failUntil[id]
	m.mu.RUnlock()
	if time.Now().Before(until) {
		return fmt.Errorf("rpc %s in backoff", id)
	}

	cli, err := m.ClientByID(id)
	if err != nil {
		m.markFail(id)
		return err
	}
	start := time.Now()
	_, err = cli.BlockNumber(ctx)
	rtt := time.Since(start).Milliseconds()
	if err != nil {
		m.markFail(id)
		return err
	}
	m.setRTT(id, rtt)
	if m.syncer != nil {
		m.syncer.SetRTT(rtt)
	}
	return nil
}

func (m *Manager) setRTT(id string, rtt int64) {
	m.mu.Lock()
	m.rtts[id] = rtt
	m.failCount[id] = 0
	delete(m.failUntil, id)
	m.mu.Unlock()
}

func (m *Manager) markFail(id string) {
	m.mu.Lock()
	m.failCount[id]++
	delete(m.rtts, id)
	if m.failCount[id] >= 2 {
		m.failUntil[id] = time.Now().Add(30 * time.Second)
	}
	m.mu.Unlock()
}

func (m *Manager) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	cli, err := m.Client()
	if err != nil {
		return nil, err
	}
	return cli.SuggestGasPrice(ctx)
}

func (m *Manager) PendingNonce(ctx context.Context, addr string) (uint64, error) {
	cli, err := m.Client()
	if err != nil {
		return 0, err
	}
	return cli.PendingNonceAt(ctx, mustAddr(addr))
}

func (m *Manager) ChainID() *big.Int {
	return big.NewInt(m.cfg.ChainID)
}

func (m *Manager) Config() *config.ChainConfig {
	return m.cfg
}

// StartPinger pings selected RPCs one-at-a-time; waits full PingIntervalSec between each.
func (m *Manager) StartPinger(ctx context.Context) {
	go func() {
		// initial primary ping for time sync
		_ = m.pingOne(ctx, m.PrimaryID(), true)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			ids := m.ActiveIDs()
			if len(ids) == 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}
			interval := time.Duration(m.cfg.PingIntervalSec) * time.Second
			if interval < time.Second {
				interval = 5 * time.Second
			}

			m.mu.Lock()
			idx := m.pingIdx % len(ids)
			m.pingIdx++
			m.mu.Unlock()

			// 每次只测 1 个，测完再等满整个 interval，不把间隔拆给多个节点
			id := ids[idx]
			_ = m.pingOne(ctx, id, id == m.PrimaryID())

			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}
