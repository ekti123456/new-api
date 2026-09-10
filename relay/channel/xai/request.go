package xai

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

func normalizeXAIRequestBody(body []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("xAI request body must be a JSON object")
	}
	for _, name := range []string{
		"client_metadata", "access_programs", "safety_identifier",
		"prompt_cache_options", "prompt_cache_retention",
	} {
		delete(fields, name)
	}

	var model string
	_ = common.Unmarshal(fields["model"], &model)
	model = strings.ToLower(strings.TrimSpace(model))
	version, _, _ := strings.Cut(strings.TrimPrefix(model, "grok-4."), "-")
	minor, versionErr := strconv.Atoi(version)
	maxEffort := ""
	if strings.HasPrefix(model, "grok-4.") && versionErr == nil && minor >= 5 && !strings.Contains(model, "non-reasoning") {
		maxEffort = "high"
		if minor >= 6 {
			maxEffort = "xhigh"
		}
		for _, name := range []string{"stop", "presence_penalty", "frequency_penalty"} {
			delete(fields, name)
		}
	}
	if err := normalizeXAIReasoningEffort(fields, "reasoning_effort", maxEffort); err != nil {
		return nil, err
	}
	for _, control := range []struct {
		name    string
		removed []string
	}{
		{name: "reasoning", removed: []string{"context", "mode"}},
		{name: "text", removed: []string{"verbosity"}},
		{name: "stream_options", removed: []string{"include_obfuscation", "reasoning_summary_delivery"}},
	} {
		raw, exists := fields[control.name]
		if !exists {
			continue
		}
		var options map[string]json.RawMessage
		if err := common.Unmarshal(raw, &options); err != nil || options == nil {
			continue
		}
		for _, name := range control.removed {
			delete(options, name)
		}
		if control.name == "reasoning" {
			if err := normalizeXAIReasoningEffort(options, "effort", maxEffort); err != nil {
				return nil, err
			}
		}
		if len(options) == 0 {
			delete(fields, control.name)
			continue
		}
		encoded, err := common.Marshal(options)
		if err != nil {
			return nil, err
		}
		fields[control.name] = encoded
	}

	var toolChoice string
	if common.Unmarshal(fields["tool_choice"], &toolChoice) == nil && (toolChoice == "auto" || toolChoice == "none") {
		var tools []json.RawMessage
		raw, exists := fields["tools"]
		if !exists || (common.Unmarshal(raw, &tools) == nil && len(tools) == 0) {
			delete(fields, "tool_choice")
		}
	}
	return common.Marshal(fields)
}

func normalizeXAIReasoningEffort(fields map[string]json.RawMessage, name, maxEffort string) error {
	if maxEffort == "" {
		return nil
	}
	var effort string
	if common.Unmarshal(fields[name], &effort) != nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(effort))
	switch normalized {
	case "none", "minimal":
		normalized = "low"
	case "max", "ultra", "xhigh":
		normalized = maxEffort
	case "low", "medium", "high":
	default:
		return nil
	}
	if normalized == effort {
		return nil
	}
	encoded, err := common.Marshal(normalized)
	if err != nil {
		return err
	}
	fields[name] = encoded
	return nil
}
