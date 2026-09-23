package common

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Default off. This is diagnostic only, never a model mapping or billing input.
var UpstreamResponseModelLogEnabled atomic.Bool

const upstreamResponseModelKey = "upstream_response_model_observation"

type UpstreamResponseModelObservation struct {
	mu       sync.Mutex
	model    string
	conflict bool
}

// A fresh observation for each upstream attempt prevents failed retries from
// supplying a model to a later response which did not declare one.
func BeginUpstreamResponseModel(c *gin.Context) *UpstreamResponseModelObservation {
	var observation *UpstreamResponseModelObservation
	if UpstreamResponseModelLogEnabled.Load() {
		observation = &UpstreamResponseModelObservation{}
	}
	c.Set(upstreamResponseModelKey, observation)
	return observation
}

func GetUpstreamResponseModel(c *gin.Context) (string, bool) {
	if c == nil || !UpstreamResponseModelLogEnabled.Load() {
		return "", false
	}
	value, _ := c.Get(upstreamResponseModelKey)
	observation, _ := value.(*UpstreamResponseModelObservation)
	if observation == nil {
		return "", false
	}
	observation.mu.Lock()
	defer observation.mu.Unlock()
	return observation.model, observation.conflict
}

// Read only protocol envelopes, not message text, tool results, echoed request
// metadata or model names synthesized by a response converter.
func (o *UpstreamResponseModelObservation) Observe(payload []byte, event string) {
	if o == nil || !UpstreamResponseModelLogEnabled.Load() {
		return
	}
	if declared := gjson.GetBytes(payload, "type").String(); declared != "" {
		event = declared
	}
	paths := []string{"response.model", "model", "modelVersion"}
	if event == "message_start" {
		paths = append(paths, "message.model")
	}
	if event == "session.created" || event == "session.updated" {
		paths = append(paths, "session.model")
	}
	model := ""
	for _, path := range paths {
		value := gjson.GetBytes(payload, path)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			model = strings.TrimSpace(value.String())
			break
		}
	}
	if model == "" || len(model) > 200 || !gjson.ValidBytes(payload) || strings.HasPrefix(model, "sk-") || strings.HasPrefix(model, "eyJ") {
		return
	}
	for _, char := range model {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-_.:/", char)) {
			return
		}
	}
	terminal := false
	switch event {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		terminal = true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.model != "" && o.model != model {
		o.conflict = true
	}
	if o.model == "" || terminal {
		o.model = model
	}
}
