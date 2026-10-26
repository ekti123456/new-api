// Package codexfork projects a validated user fork onto an independent root.
// Callers own authentication and graph validation. This package only edits the
// protocol envelope; input, tools and encrypted history remain byte-for-byte.
package codexfork

import (
	"bytes"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unsafe"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const Version = 1
const maxMetadataBytes = 1 << 20

var ErrEnvelope = errors.New("invalid independent conversation metadata")

// Project is called only after the caller has proved that root is the user
// thread, not a background leaf. It intentionally does not inspect input.
func Project(headers http.Header, body []byte, root, turn string) (http.Header, []byte, error) {
	trimmed := bytes.TrimSpace(body)
	if root == "" || len(trimmed) == 0 || trimmed[0] != '{' || !gjson.ValidBytes(body) {
		return nil, nil, ErrEnvelope
	}
	outHeaders := headers.Clone()
	if outHeaders == nil {
		outHeaders = make(http.Header)
	}
	// The caller has validated Session-Id as either the fork cache source or
	// its new root. An additional legacy carrier cannot silently name a third
	// conversation and get overwritten by normalization.
	cacheSource := strings.TrimSpace(headers.Get("Session-Id"))
	for _, name := range []string{"Session-Id", "Session_id", "X-Session-ID", "OpenAI-Session-ID", "Conversation-Id", "Conversation_id"} {
		for _, value := range headers.Values(name) {
			value = strings.TrimSpace(value)
			if value != "" && !strings.EqualFold(value, root) && (cacheSource == "" || !strings.EqualFold(value, cacheSource)) {
				return nil, nil, ErrEnvelope
			}
		}
		if outHeaders.Get(name) != "" || name == "Session-Id" {
			outHeaders.Set(name, root)
		}
	}
	for _, name := range []string{"X-Codex-Forked-From-Thread-Id", "X-Codex-Parent-Thread-Id"} {
		outHeaders.Del(name)
	}
	if raw := outHeaders.Get("X-Codex-Turn-Metadata"); raw != "" {
		cleaned, err := projectMetadata(raw, root, turn, 0)
		if err != nil {
			return nil, nil, err
		}
		outHeaders.Set("X-Codex-Turn-Metadata", cleaned)
	}
	// The input is borrowed read-only until Project returns. Avoid converting a
	// multi-megabyte image history to an owning string just to visit its envelope.
	view := unsafe.String(unsafe.SliceData(body), len(body))
	var metadata, cache gjson.Result
	duplicate := false
	gjson.Parse(view).ForEach(func(key, value gjson.Result) bool {
		switch key.String() {
		case "client_metadata":
			if metadata.Exists() {
				duplicate = true
				return false
			}
			metadata = value
		case "prompt_cache_key":
			if cache.Exists() {
				duplicate = true
				return false
			}
			cache = value
		}
		return true
	})
	if duplicate {
		return nil, nil, ErrEnvelope
	}
	var changes []replacement
	if metadata.Exists() {
		cleaned, err := projectMetadata(metadata.Raw, root, turn, 0)
		if err != nil {
			return nil, nil, err
		}
		if cleaned != metadata.Raw {
			changes = append(changes, replacement{metadata.Index, metadata.Index + len(metadata.Raw), []byte(cleaned)})
		}
	}
	encoded, _ := common.Marshal(root)
	if cache.Exists() {
		if cache.Type != gjson.String || cache.String() != root {
			changes = append(changes, replacement{cache.Index, cache.Index + len(cache.Raw), encoded})
		}
	} else {
		end := len(body) - 1
		for end >= 0 && strings.ContainsRune(" \r\n\t", rune(body[end])) {
			end--
		}
		if end < 0 || body[end] != '}' {
			return nil, nil, ErrEnvelope
		}
		prefix := []byte(`,"prompt_cache_key":`)
		if bytes.Equal(bytes.TrimSpace(body[:end]), []byte("{")) {
			prefix = prefix[1:]
		}
		changes = append(changes, replacement{end, end, append(prefix, encoded...)})
	}
	return outHeaders, replace(body, changes), nil
}

type replacement struct {
	start, end int
	value      []byte
}

// Changes are collected before allocating the body, so large embedded images
// are copied once rather than once per identity field.
func replace(body []byte, changes []replacement) []byte {
	if len(changes) == 0 {
		return body
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].start < changes[j].start })
	size := len(body)
	for _, change := range changes {
		size += len(change.value) - (change.end - change.start)
	}
	out := make([]byte, 0, size)
	position := 0
	for _, change := range changes {
		out = append(out, body[position:change.start]...)
		out = append(out, change.value...)
		position = change.end
	}
	return append(out, body[position:]...)
}

func projectMetadata(raw, root, turn string, depth int) (string, error) {
	if depth > 3 || len(raw) > maxMetadataBytes || !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		return "", ErrEnvelope
	}
	result := raw
	var failure error
	seen := make(map[string]bool)
	gjson.Parse(raw).ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		if seen[name] {
			failure = ErrEnvelope
			return false
		}
		seen[name] = true
		switch name {
		case "forked_from_thread_id", "forked_from_ordinal_exclusive", "x-codex-forked-from-thread-id", "x_codex_forked_from_thread_id", "parent_thread_id", "x-codex-parent-thread-id", "x_codex_parent_thread_id", "parent_turn_id", "guardian_classifier_source_thread_id":
			result, failure = sjson.Delete(result, name)
		case "session_id":
			result, failure = sjson.Set(result, name, root)
		case "thread_source":
			result, failure = sjson.Set(result, name, "user")
		case "root_turn_id":
			if turn != "" {
				result, failure = sjson.Set(result, name, turn)
			} else {
				result, failure = sjson.Delete(result, name)
			}
		case "history_initialization":
			if value.String() == "warm_fork" || value.String() == "fork" {
				result, failure = sjson.Delete(result, name)
			}
		case "x-codex-turn-metadata", "x_codex_turn_metadata", "client_metadata":
			nested := value.Raw
			if value.Type == gjson.String {
				nested = value.String()
			}
			var cleaned string
			cleaned, failure = projectMetadata(nested, root, turn, depth+1)
			if failure == nil {
				if value.Type == gjson.String {
					result, failure = sjson.Set(result, name, cleaned)
				} else {
					result, failure = sjson.SetRaw(result, name, cleaned)
				}
			}
		}
		return failure == nil
	})
	if failure == nil && !seen["thread_source"] {
		result, failure = sjson.Set(result, "thread_source", "user")
	}
	return result, failure
}
