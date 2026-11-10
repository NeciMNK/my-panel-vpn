package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
	"github.com/mhsanaei/3x-ui/v3/internal/util/wirecodec"
)

// XrayProxyResponse is a node's raw reply to a proxied Xray-settings request.
type XrayProxyResponse struct {
	Status      int
	ContentType string
	Body        []byte
}

// remoteXrayProxyTimeout covers outbound tests and WARP registration, which the
// node itself runs against third-party hosts before answering.
const remoteXrayProxyTimeout = 60 * time.Second

// ProxyXray forwards one request to the node's /panel/api/xray/<subPath> so the
// master edits a node's outbounds, routing, balancers and WARP with the node's own handlers.
func (r *Remote) ProxyXray(ctx context.Context, method, subPath, rawQuery, contentType string, body []byte) (*XrayProxyResponse, error) {
	if r.node.ApiToken == "" && r.node.TlsVerifyMode != "mtls" {
		return nil, errors.New("node has no API token configured")
	}
	if strings.Contains(subPath, "..") {
		return nil, fmt.Errorf("invalid xray path %q", subPath)
	}
	base, err := r.baseURL()
	if err != nil {
		return nil, err
	}
	target := base + "panel/api/xray/" + strings.TrimPrefix(subPath, "/")
	if rawQuery != "" {
		target += "?" + rawQuery
	}

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}
	cctx, cancel := context.WithTimeout(netsafe.ContextWithAllowPrivate(ctx, r.node.AllowPrivateAddress), remoteXrayProxyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, method, target, reqBody)
	if err != nil {
		return nil, err
	}
	if r.node.ApiToken != "" {
		token, err := nodetoken.Decrypt(r.node.Id, r.node.ApiToken)
		if err != nil {
			return nil, fmt.Errorf("decrypt node token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(wirecodec.MasterPushHeader, "1")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if len(body) > 0 {
		req.Header.Set(wirecodec.HashHeader, wirecodec.Sha256Hex(body))
	}

	client, err := r.httpClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s xray/%s: %w", method, subPath, err)
	}
	defer resp.Body.Close()
	r.recordCaps(resp.Header)

	raw, err := readCappedBody(resp.Body, maxRemoteResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &XrayProxyResponse{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        raw,
	}, nil
}
