package channel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// applyCPAIdentity signs only configured CPA destinations, after body and header
// transformations. The asserted IDs come from authenticated relay state.
func applyCPAIdentity(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-cpa-identity") {
			req.Header.Del(name)
		}
	}
	targets := strings.TrimSpace(os.Getenv("CPA_IDENTITY_TARGETS"))
	matched := false
	for target := range strings.SplitSeq(targets, ",") {
		u, err := url.Parse(strings.TrimSpace(target))
		if err == nil && u.User == nil && u.Host != "" && strings.EqualFold(u.Scheme, req.URL.Scheme) && strings.EqualFold(u.Host, req.URL.Host) && (u.Path == "" || u.Path == "/" || req.URL.Path == strings.TrimSuffix(u.Path, "/") || strings.HasPrefix(req.URL.Path, strings.TrimSuffix(u.Path, "/")+"/")) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	secret, instance := os.Getenv("CPA_IDENTITY_SIGNING_SECRET"), strings.TrimSpace(os.Getenv("CPA_IDENTITY_INSTANCE_ID"))
	if len(secret) < 32 || instance == "" {
		return fmt.Errorf("CPA identity requires a persistent instance ID and a signing secret of at least 32 bytes")
	}
	if info.UserId <= 0 || info.TokenId <= 0 {
		return fmt.Errorf("CPA identity requires authenticated user and token IDs")
	}
	credential := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	if credential == "" {
		return fmt.Errorf("CPA identity requires the selected channel credential")
	}
	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return fmt.Errorf("CPA identity requires a replayable outgoing body")
		}
		r, err := req.GetBody()
		if err != nil {
			return fmt.Errorf("read CPA signing body: %w", err)
		}
		body, err = io.ReadAll(r)
		closeErr := r.Close()
		if err != nil {
			return fmt.Errorf("read CPA signing body: %w", err)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	device := ""
	for _, name := range []string{"X-Codex-Installation-Id", "X-Device-Id", "Oai-Device-Id"} {
		if device = strings.TrimSpace(c.GetHeader(name)); device != "" {
			break
		}
	}
	if device == "" {
		for _, path := range []string{"client_metadata.x-codex-installation-id", "client_metadata.installation_id", "client_metadata.device_id", "metadata.device_id"} {
			if device = strings.TrimSpace(gjson.GetBytes(body, path).String()); device != "" {
				break
			}
		}
	}
	if device == "" {
		device = strings.TrimSpace(gjson.Get(c.GetHeader("X-Codex-Turn-Metadata"), "installation_id").String())
	}
	ua := c.Request.UserAgent()
	if len(ua) > 4096 || len(device) > 1024 {
		return fmt.Errorf("CPA identity UA or device ID is too long")
	}
	bodyHash := sha256.Sum256(body)
	instanceHash := sha256.Sum256([]byte(instance))
	audJSON, err := common.Marshal([]string{"cpa-audience-v1", credential})
	if err != nil {
		return err
	}
	audHash := sha256.Sum256(audJSON)
	claim := map[string]any{
		"v": 1, "instance": hex.EncodeToString(instanceHash[:]),
		"user": strconv.Itoa(info.UserId), "key": strconv.Itoa(info.TokenId),
		"ua": ua, "device": device, "ts": time.Now().Unix(), "nonce": uuid.NewString(),
		"aud": hex.EncodeToString(audHash[:]), "body_sha256": hex.EncodeToString(bodyHash[:]),
		"request_id": info.RequestId,
	}
	b, err := common.Marshal(claim)
	if err != nil {
		return err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join([]string{"cpa-identity-v1", req.Method, req.URL.RequestURI(), raw}, "\n")))
	req.Header.Set("X-CPA-Identity", raw)
	req.Header.Set("X-CPA-Identity-Signature", hex.EncodeToString(mac.Sum(nil)))
	return nil
}
