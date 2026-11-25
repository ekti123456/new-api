package wsmanager

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"
)

var (
	ErrDraining             = errors.New("Responses WebSocket service is shutting down")
	ErrConnectionLimit      = errors.New("Responses WebSocket connection limit reached")
	ErrUserLimit            = errors.New("user Responses WebSocket connection limit reached")
	ErrClientHandshakeLimit = errors.New("client Responses WebSocket handshake limit reached")
	ErrHandshakeLimit       = errors.New("Responses WebSocket handshake limit reached")
	ErrChannelLimit         = errors.New("channel Responses WebSocket connection limit reached")
	LocalResponses          = NewResponsesManager()
)

type sessionPhase uint8

const (
	upgrading sessionPhase = iota
	waitingFirst
	active
	idle
	closing
	released
)

// Process-local lifecycle is separate from the cross-instance channel revocation
// registry. A rolling restart must only drain sockets owned by this process.
type ResponsesManager struct {
	mu              sync.Mutex
	settings        ResponsesSettings
	sessions        map[*ResponsesSession]struct{}
	users, channels map[int]int
	drained         chan struct{}

	counts                                 [released]int
	pending, connected, peak               int
	opened, ended                          uint64
	dialAttempts, dialErrors, dialCanceled uint64
	rejections, reasons                    map[string]uint64
}

type ResponsesSession struct {
	manager            *ResponsesManager
	ctx                context.Context
	cancel             context.CancelFunc
	user, channel      int
	phase              sessionPhase
	dialing, connected bool
	idleSince          time.Time
	timer              *time.Timer
	timerGeneration    uint64
	reason             string
}

func NewResponsesManager() *ResponsesManager {
	return &ResponsesManager{
		settings:   DefaultResponsesSettings(),
		sessions:   make(map[*ResponsesSession]struct{}),
		users:      make(map[int]int),
		channels:   make(map[int]int),
		rejections: make(map[string]uint64),
		reasons:    make(map[string]uint64),
	}
}

func (m *ResponsesManager) Open(ctx context.Context, user int) (*ResponsesSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.drained != nil:
		m.rejections["shutdown"]++
		return nil, ErrDraining
	case m.settings.MaxConnections > 0 && len(m.sessions) >= m.settings.MaxConnections:
		m.rejections["instance"]++
		return nil, ErrConnectionLimit
	case user > 0 && m.settings.MaxUserConnections > 0 && m.users[user] >= m.settings.MaxUserConnections:
		m.rejections["user"]++
		return nil, ErrUserLimit
	case m.settings.MaxClientHandshakes > 0 && m.counts[upgrading] >= m.settings.MaxClientHandshakes:
		m.rejections["client_handshakes"]++
		return nil, ErrClientHandshakeLimit
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &ResponsesSession{manager: m, user: user, ctx: ctx, cancel: cancel}
	m.sessions[s] = struct{}{}
	m.users[user]++
	m.counts[upgrading]++
	m.opened++
	m.peak = max(m.peak, len(m.sessions))
	return s, nil
}

func (s *ResponsesSession) Context() context.Context    { return s.ctx }
func (s *ResponsesSession) Settings() ResponsesSettings { return s.manager.Settings() }

func (s *ResponsesSession) transitionLocked(to sessionPhase) {
	s.manager.counts[s.phase]--
	s.phase = to
	if to != released {
		s.manager.counts[to]++
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
		s.timerGeneration++
	}
}

func (s *ResponsesSession) armIdleLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.timerGeneration++
	generation := s.timerGeneration
	seconds, reason := s.manager.settings.IdleSeconds, "idle_timeout"
	if s.phase == waitingFirst {
		seconds, reason = s.manager.settings.FirstFrameSeconds, "first_frame_timeout"
	}
	if seconds <= 0 {
		return
	}
	s.timer = time.AfterFunc(time.Until(s.idleSince.Add(time.Duration(seconds)*time.Second)), func() {
		m := s.manager
		m.mu.Lock()
		defer m.mu.Unlock()
		if s.timerGeneration == generation && (s.phase == waitingFirst || s.phase == idle) {
			s.closeLocked(reason)
		}
	})
}

func (s *ResponsesSession) Upgraded() {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.phase != upgrading {
		return
	}
	s.transitionLocked(waitingFirst)
	s.idleSince = time.Now()
	s.armIdleLocked()
}

func (s *ResponsesSession) BeginTurn() bool {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.drained != nil || (s.phase != waitingFirst && s.phase != idle) {
		return false
	}
	s.transitionLocked(active)
	return true
}

// Called after middleware, accounting and request slot release finish.
func (s *ResponsesSession) EndTurn() {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.phase != active {
		return
	}
	if m.drained != nil {
		s.closeLocked("shutdown")
		return
	}
	s.transitionLocked(idle)
	s.idleSince = time.Now()
	s.armIdleLocked()
}

func (s *ResponsesSession) BeginDial(channel int) error {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if s.dialing || s.connected {
		return errors.New("upstream connection already reserved")
	}
	if m.settings.MaxHandshakes > 0 && m.pending >= m.settings.MaxHandshakes {
		m.rejections["handshakes"]++
		return ErrHandshakeLimit
	}
	if m.settings.MaxChannelConnections > 0 && m.channels[channel] >= m.settings.MaxChannelConnections {
		m.rejections["channel"]++
		return ErrChannelLimit
	}
	s.dialing = true
	s.channel = channel
	m.pending++
	m.channels[channel]++
	m.dialAttempts++
	return nil
}

func decrementCount(counts map[int]int, id int) {
	if counts[id] <= 1 {
		delete(counts, id)
	} else {
		counts[id]--
	}
}

func (s *ResponsesSession) FinishDial(connected bool) {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.dialing {
		return
	}
	s.dialing = false
	m.pending--
	if connected {
		s.connected = true
		m.connected++
		return
	}
	if s.ctx.Err() != nil {
		m.dialCanceled++
	} else {
		m.dialErrors++
	}
	decrementCount(m.channels, s.channel)
}

func (s *ResponsesSession) UpstreamClosed() {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.connected {
		s.connected = false
		m.connected--
		decrementCount(m.channels, s.channel)
	}
}

func (s *ResponsesSession) closeLocked(reason string) {
	if s.phase >= closing {
		return
	}
	s.transitionLocked(closing)
	if s.reason == "" {
		s.reason = reason
	}
	s.cancel()
}

// Remember a policy close before notifying the peer, which can immediately
// acknowledge it and end the reader before the close writer returns.
func (s *ResponsesSession) NoteCloseReason(reason string) {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.reason == "" {
		s.reason = reason
	}
}

func (s *ResponsesSession) Close(reason string) {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	s.closeLocked(reason)
}

func (s *ResponsesSession) CloseReason() string {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	return s.reason
}

// A session remains counted until the socket handlers and billing workers exit.
func (s *ResponsesSession) Release() {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.phase == released {
		return
	}
	s.closeLocked("client_closed")
	if s.dialing {
		m.pending--
		m.dialCanceled++
	}
	if s.connected {
		m.connected--
	}
	if s.dialing || s.connected {
		decrementCount(m.channels, s.channel)
	}
	s.dialing, s.connected = false, false
	s.transitionLocked(released)
	delete(m.sessions, s)
	decrementCount(m.users, s.user)
	m.ended++
	m.reasons[s.reason]++
	if m.drained != nil && len(m.sessions) == 0 {
		close(m.drained)
	}
}

func (m *ResponsesManager) BeginDrain() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.drained != nil {
		return m.drained
	}
	m.drained = make(chan struct{})
	for s := range m.sessions {
		if s.phase != active {
			s.closeLocked("shutdown")
		}
	}
	if len(m.sessions) == 0 {
		close(m.drained)
	}
	return m.drained
}

func (m *ResponsesManager) ForceClose() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s := range m.sessions {
		s.closeLocked("shutdown_timeout")
	}
}

type ResponsesStats struct {
	Current             int               `json:"current"`
	MaxConnections      int               `json:"max_connections"`
	MaxClientHandshakes int               `json:"max_client_handshakes"`
	MaxHandshakes       int               `json:"max_handshakes"`
	Peak                int               `json:"peak"`
	ClientHandshakes    int               `json:"client_handshakes"`
	WaitingFirst        int               `json:"waiting_first"`
	Active              int               `json:"active"`
	Idle                int               `json:"idle"`
	Closing             int               `json:"closing"`
	UpstreamHandshakes  int               `json:"upstream_handshakes"`
	UpstreamConnections int               `json:"upstream_connections"`
	Opened              uint64            `json:"opened"`
	Released            uint64            `json:"released"`
	DialAttempts        uint64            `json:"dial_attempts"`
	DialErrors          uint64            `json:"dial_errors"`
	DialCanceled        uint64            `json:"dial_canceled"`
	Draining            bool              `json:"draining"`
	Rejections          map[string]uint64 `json:"rejections"`
	ReleaseReasons      map[string]uint64 `json:"release_reasons"`
}

func (m *ResponsesManager) Stats() ResponsesStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ResponsesStats{
		Current:             len(m.sessions),
		MaxConnections:      m.settings.MaxConnections,
		MaxClientHandshakes: m.settings.MaxClientHandshakes,
		MaxHandshakes:       m.settings.MaxHandshakes,
		Peak:                m.peak,
		ClientHandshakes:    m.counts[upgrading],
		WaitingFirst:        m.counts[waitingFirst],
		Active:              m.counts[active],
		Idle:                m.counts[idle],
		Closing:             m.counts[closing],
		UpstreamHandshakes:  m.pending,
		UpstreamConnections: m.connected,
		Opened:              m.opened,
		Released:            m.ended,
		DialAttempts:        m.dialAttempts,
		DialErrors:          m.dialErrors,
		DialCanceled:        m.dialCanceled,
		Draining:            m.drained != nil,
		Rejections:          maps.Clone(m.rejections),
		ReleaseReasons:      maps.Clone(m.reasons),
	}
}
