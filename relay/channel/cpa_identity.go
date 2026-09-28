package channel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// applyCPAIdentity forwards routing metadata after channel header overrides.
// User and token IDs come from authenticated relay state, never client headers.
// This metadata does not authenticate the request or grant CPA permissions.
func applyCPAIdentity(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-cpa-identity") {
			delete(req.Header, name)
		}
	}
	instance := CPAIdentityInstanceID()
	if instance == "" || info.UserId <= 0 || info.TokenId <= 0 {
		return nil
	}
	device := ""
	for _, name := range []string{"X-Codex-Installation-Id", "X-Device-Id", "Oai-Device-Id"} {
		if device = strings.TrimSpace(c.GetHeader(name)); device != "" {
			break
		}
	}
	if device == "" {
		if cached, ok := c.Get("cpa_identity_body_device"); ok {
			device, _ = cached.(string)
		} else {
			device = cpaBodyDevice(c)
			c.Set("cpa_identity_body_device", device)
		}
	}
	if device == "" {
		device = strings.TrimSpace(gjson.Get(c.GetHeader("X-Codex-Turn-Metadata"), "installation_id").String())
	}
	ua := c.Request.UserAgent()
	if len(ua) > 4096 || len(device) > 1024 || len(info.RequestId) > 256 {
		return nil
	}
	instanceHash := sha256.Sum256([]byte(instance))
	claim := map[string]any{
		"v": 1, "instance": hex.EncodeToString(instanceHash[:]),
		"user": strconv.Itoa(info.UserId), "key": strconv.Itoa(info.TokenId),
		"ua": ua, "request_id": info.RequestId,
	}
	if device != "" {
		claim["device"] = device
	}
	b, err := common.Marshal(claim)
	if err != nil {
		return err
	}
	req.Header.Set("X-CPA-Identity", base64.RawURLEncoding.EncodeToString(b))
	return nil
}

// cpaBodyDevice reads only cached original JSON through an independent reader.
// It never consumes the outgoing body or requires uploads to be replayable.
func cpaBodyDevice(c *gin.Context) string {
	if !strings.Contains(strings.ToLower(c.GetHeader("Content-Type")), "json") {
		return ""
	}
	cached, ok := c.Get(common.KeyBodyStorage)
	if !ok {
		return ""
	}
	storage, ok := cached.(common.BodyStorage)
	if !ok {
		return ""
	}
	reader, err := storage.NewReader()
	if err != nil {
		return ""
	}
	defer reader.Close()
	var metadata struct {
		ClientMetadata common.RawMessage `json:"client_metadata"`
		Metadata       common.RawMessage `json:"metadata"`
	}
	if common.DecodeJson(reader, &metadata) != nil {
		return ""
	}
	for _, path := range []string{"x-codex-installation-id", "installation_id", "device_id"} {
		if device := strings.TrimSpace(gjson.GetBytes(metadata.ClientMetadata, path).String()); device != "" {
			return device
		}
	}
	return strings.TrimSpace(gjson.GetBytes(metadata.Metadata, "device_id").String())
}
