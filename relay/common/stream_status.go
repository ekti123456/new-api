package common

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	delivery  StreamDeliveryStatus

	mu         sync.Mutex
	Errors     []StreamErrorEntry
	ErrorCount int
}

type StreamDeliveryStatus struct {
	TerminalEvent      string `json:"terminal_event,omitempty"`
	ResponseStatus     string `json:"response_status,omitempty"`
	IncompleteReason   string `json:"incomplete_reason,omitempty"`
	TerminalReceivedAt int64  `json:"terminal_received_at_unix_ms,omitempty"`
	TerminalFlushedAt  int64  `json:"terminal_flushed_at_unix_ms,omitempty"`
	ClientCanceledAt   int64  `json:"client_canceled_at_unix_ms,omitempty"`
	TerminalWrite      string `json:"terminal_write,omitempty"`
	UsageSource        string `json:"usage_source,omitempty"`
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if reason == StreamEndReasonClientGone && s.delivery.ClientCanceledAt == 0 {
		s.delivery.ClientCanceledAt = time.Now().UnixMilli()
	}
	terminalFlushed := s.delivery.TerminalEvent != "" && s.delivery.TerminalWrite == "flushed"
	if s.EndReason == StreamEndReasonNone || (reason == StreamEndReasonDone && s.EndReason == StreamEndReasonClientGone && terminalFlushed) {
		s.EndReason = reason
		s.EndError = err
	}
}

func (status *StreamStatus) ObserveTerminal(eventType, responseStatus, incompleteReason string) {
	if status == nil {
		return
	}
	status.mu.Lock()
	defer status.mu.Unlock()
	status.delivery.TerminalEvent = eventType
	status.delivery.ResponseStatus = responseStatus
	status.delivery.IncompleteReason = incompleteReason
	status.delivery.TerminalReceivedAt = time.Now().UnixMilli()
	status.delivery.TerminalWrite = "pending"
}

func (status *StreamStatus) RecordTerminalWrite(err error) {
	if status == nil {
		return
	}
	status.mu.Lock()
	defer status.mu.Unlock()
	if err != nil {
		status.delivery.TerminalWrite = "failed"
		return
	}
	status.delivery.TerminalWrite = "flushed"
	status.delivery.TerminalFlushedAt = time.Now().UnixMilli()
}

func (status *StreamStatus) SetUsageSource(source string) {
	if status == nil {
		return
	}
	status.mu.Lock()
	defer status.mu.Unlock()
	status.delivery.UsageSource = source
}

func (status *StreamStatus) DeliverySnapshot() StreamDeliveryStatus {
	if status == nil {
		return StreamDeliveryStatus{}
	}
	status.mu.Lock()
	defer status.mu.Unlock()
	return status.delivery
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	return b.String()
}
