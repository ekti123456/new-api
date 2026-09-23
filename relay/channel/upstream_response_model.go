package channel

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Bound optional diagnostics independently of the response size. An oversized
// JSON body/event is skipped, never truncated or rejected on the relay path.
const responseModelObservationLimit = 2 << 20

type responseModelBody struct {
	io.ReadCloser
	observation   *common.UpstreamResponseModelObservation
	sse           bool
	buffer        []byte
	data          []byte
	event         string
	overflow      bool
	eventOverflow bool
}

func observeResponseModelBody(resp *http.Response, observation *common.UpstreamResponseModelObservation) {
	if observation == nil || resp == nil || resp.Body == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	sse := strings.HasPrefix(contentType, "text/event-stream")
	if !sse && !strings.Contains(contentType, "json") && contentType != "" {
		return
	}
	resp.Body = &responseModelBody{ReadCloser: resp.Body, observation: observation, sse: sse}
}

func (b *responseModelBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if !common.UpstreamResponseModelLogEnabled.Load() {
		b.buffer, b.data = nil, nil
		return n, err
	}
	if !b.sse {
		if !b.overflow && len(b.buffer)+n <= responseModelObservationLimit {
			b.buffer = append(b.buffer, p[:n]...)
		} else {
			b.buffer, b.overflow = nil, true
		}
		if err == io.EOF && !b.overflow {
			b.observation.Observe(b.buffer, "")
			b.buffer = nil
		}
		return n, err
	}
	remaining := p[:n]
	for len(remaining) > 0 {
		end := bytes.IndexByte(remaining, '\n')
		if end < 0 {
			b.appendLine(remaining)
			break
		}
		b.appendLine(remaining[:end])
		b.finishLine()
		remaining = remaining[end+1:]
	}
	if err == io.EOF {
		if len(b.buffer) > 0 || b.overflow {
			b.finishLine()
		}
		b.finishEvent()
	}
	return n, err
}

func (b *responseModelBody) appendLine(value []byte) {
	if b.overflow || len(b.buffer)+len(value) > responseModelObservationLimit {
		b.buffer, b.overflow = nil, true
		return
	}
	b.buffer = append(b.buffer, value...)
}

func (b *responseModelBody) finishLine() {
	line := bytes.TrimSuffix(b.buffer, []byte{'\r'})
	if b.overflow {
		b.eventOverflow = true
	} else if len(line) == 0 {
		b.finishEvent()
	} else if bytes.HasPrefix(line, []byte("data:")) && !b.eventOverflow {
		value := bytes.TrimPrefix(line[5:], []byte{' '})
		if len(b.data)+len(value)+1 <= responseModelObservationLimit {
			b.data = append(b.data, value...)
			b.data = append(b.data, '\n')
		} else {
			b.data, b.eventOverflow = nil, true
		}
	} else if bytes.HasPrefix(line, []byte("event:")) && len(line) < 128 {
		b.event = strings.TrimSpace(string(line[6:]))
	}
	b.buffer, b.overflow = b.buffer[:0], false
}

func (b *responseModelBody) finishEvent() {
	if !b.eventOverflow && len(b.data) > 0 {
		b.observation.Observe(b.data, b.event)
	}
	b.data, b.event, b.eventOverflow = b.data[:0], "", false
}
