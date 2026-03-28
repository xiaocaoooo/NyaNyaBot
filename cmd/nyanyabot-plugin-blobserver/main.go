package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

type BlobServer struct {
	mu  sync.RWMutex
	cfg struct {
		BlobServer    string `json:"blob_server"`
		BlobToken     string `json:"blob_token"`
		BlobOneBotURL string `json:"blob_onebot_url"`
	}
}

func (b *BlobServer) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"blob_server":{"type":"string","description":"Blob Server 域名/地址"},
			"blob_token":{"type":"string","description":"Blob Server Token（Bearer）"},
			"blob_onebot_url":{"type":"string","description":"发送给 OneBot 的 Blob URL 前缀"}
		},
		"additionalProperties":true
	}`)
	def := json.RawMessage(`{"blob_server":"","blob_token":"","blob_onebot_url":""}`)

	return papi.Descriptor{
		Name:         "BlobServer",
		PluginID:     "external.blobserver",
		Version:      "0.1.0",
		Author:       "nyanyabot",
		Description:  "Blob upload helper plugin",
		Dependencies: []string{},
		Exports: []papi.ExportSpec{
			{
				Name:         "blob.upload_remote",
				Description:  "下载远程文件并上传到 Blob Server，返回 Blob URL 与 OneBot URL",
				ParamsSchema: json.RawMessage(`{"type":"object","properties":{"download_url":{"type":"string"},"blob_id":{"type":"string"},"kind":{"type":"string"}},"required":["download_url","blob_id"],"additionalProperties":false}`),
				ResultSchema: json.RawMessage(`{"type":"object","properties":{"blob_url":{"type":"string"},"onebot_url":{"type":"string"}},"required":["blob_url","onebot_url"],"additionalProperties":false}`),
			},
		},
		Config:   &papi.ConfigSpec{Version: "1", Description: "Blob plugin config", Schema: schema, Default: def},
		Commands: []papi.CommandListener{},
		Events:   []papi.EventListener{},
	}, nil
}

func (b *BlobServer) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	parsed := struct {
		BlobServer    string `json:"blob_server"`
		BlobToken     string `json:"blob_token"`
		BlobOneBotURL string `json:"blob_onebot_url"`
	}{}
	if len(config) > 0 {
		_ = json.Unmarshal(config, &parsed)
	}

	b.mu.Lock()
	b.cfg.BlobServer = strings.TrimSpace(parsed.BlobServer)
	b.cfg.BlobToken = strings.TrimSpace(parsed.BlobToken)
	b.cfg.BlobOneBotURL = strings.TrimSpace(parsed.BlobOneBotURL)
	b.mu.Unlock()
	return nil
}

func (b *BlobServer) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = callerPluginID
	if method != "blob.upload_remote" {
		return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "method is not exported")
	}

	var req struct {
		DownloadURL string `json:"download_url"`
		BlobID      string `json:"blob_id"`
		Kind        string `json:"kind"`
	}
	if err := json.Unmarshal(paramsJSON, &req); err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, "params must be a JSON object")
	}
	req.DownloadURL = strings.TrimSpace(req.DownloadURL)
	req.BlobID = strings.TrimSpace(req.BlobID)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.DownloadURL == "" {
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, "download_url is required")
	}
	if req.BlobID == "" {
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, "blob_id is required")
	}
	if req.Kind == "" {
		req.Kind = "file"
	}

	b.mu.RLock()
	blobServer := b.cfg.BlobServer
	blobToken := b.cfg.BlobToken
	blobOneBotURL := b.cfg.BlobOneBotURL
	b.mu.RUnlock()
	if blobServer == "" {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, "blob_server is not configured")
	}

	path, filename, err := downloadToTemp(ctx, req.DownloadURL, req.BlobID, req.Kind)
	if err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, err.Error())
	}
	defer os.Remove(path)

	if err := uploadFileToBlob(ctx, blobServer, blobToken, req.BlobID, path, filename); err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, err.Error())
	}

	blobURL := buildBlobURL(blobServer, req.BlobID)
	oneBotURL := blobURL
	if blobOneBotURL != "" {
		oneBotURL = rewriteBlobURLForOneBot(oneBotURL, blobServer, blobOneBotURL)
	}
	if blobToken != "" {
		oneBotURL = appendTokenToURL(oneBotURL, blobToken)
	}

	out, err := json.Marshal(map[string]string{
		"blob_url":   blobURL,
		"onebot_url": oneBotURL,
	})
	if err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, err.Error())
	}
	return out, nil
}

func (b *BlobServer) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	_ = listenerID
	_ = eventRaw
	_ = match
	return papi.HandleResult{}, nil
}

func (b *BlobServer) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func appendTokenToURL(u string, token string) string {
	u = strings.TrimSpace(u)
	if u == "" || token == "" {
		return u
	}
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	q := pu.Query()
	if q.Get("token") == "" {
		q.Set("token", token)
		pu.RawQuery = q.Encode()
	}
	return pu.String()
}

func buildBlobURL(blobServer string, id string) string {
	base := normalizeHTTPBase(blobServer)
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/blobs/" + url.PathEscape(id)
	u.RawQuery = ""
	return u.String()
}

func downloadToTemp(ctx context.Context, downloadURL string, id string, kind string) (string, string, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "nyanyabot-plugin-blobserver/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return "", "", fmt.Errorf("download failed: %s url=%s", resp.Status, downloadURL)
	}

	filename := id + ".bin"
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if fn, ok := params["filename"]; ok && strings.TrimSpace(fn) != "" {
				filename = fn
			}
		}
	}
	if filename == id+".bin" {
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			if exts, err := mime.ExtensionsByType(ct); err == nil && len(exts) > 0 {
				filename = id + exts[0]
			}
		}
	}
	if filename == id+".bin" {
		switch kind {
		case "image":
			filename = id + ".png"
		case "video":
			filename = id + ".mp4"
		}
	}

	dir := "/tmp/nyanyabot-blob"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp(dir, "nyanyabot-blobserver-*.tmp")
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", "", err
	}
	return f.Name(), filename, nil
}

func blobPrepare(ctx context.Context, blobServer string, blobToken string, id string) (bool, error) {
	base := normalizeHTTPBase(blobServer)
	u, err := url.Parse(base)
	if err != nil {
		return false, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/blobs/prepare"
	prepareURL := u.String()

	body, _ := json.Marshal(map[string]string{"id": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prepareURL, strings.NewReader(string(body)))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if blobToken != "" {
		req.Header.Set("Authorization", "Bearer "+blobToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return false, fmt.Errorf("blob prepare failed: %s url=%s", resp.Status, prepareURL)
	}

	var out struct {
		UploadRequired bool `json:"upload_required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.UploadRequired, nil
}

func uploadFileToBlob(ctx context.Context, blobServer string, blobToken string, id string, filePath string, filename string) error {
	uploadRequired, err := blobPrepare(ctx, blobServer, blobToken, id)
	if err != nil {
		return err
	}
	if !uploadRequired {
		return nil
	}

	base := normalizeHTTPBase(blobServer)
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/blobs/" + url.PathEscape(id)
	uploadURL := u.String()

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer writer.Close()

		fw, err := writer.CreateFormFile("file", filepath.Base(filename))
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(fw, file); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if blobToken != "" {
		req.Header.Set("Authorization", "Bearer "+blobToken)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("blob upload failed: %s url=%s", resp.Status, uploadURL)
	}
	return nil
}

func rewriteBlobURLForOneBot(u string, blobServer string, blobOneBotURL string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	base := normalizeHTTPBase(blobServer)
	if base == "" {
		return u
	}
	if !strings.HasPrefix(u, base) {
		return u
	}
	newBase := normalizeHTTPBase(blobOneBotURL)
	if newBase == "" {
		return u
	}
	return newBase + strings.TrimPrefix(u, base)
}

func normalizeHTTPBase(hostOrURL string) string {
	hostOrURL = strings.TrimSpace(hostOrURL)
	if hostOrURL == "" {
		return ""
	}
	if strings.HasPrefix(hostOrURL, "http://") || strings.HasPrefix(hostOrURL, "https://") {
		return strings.TrimRight(hostOrURL, "/")
	}
	return "http://" + strings.TrimRight(hostOrURL, "/")
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "nyanyabot-plugin-blobserver", Level: hclog.Info})

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake(),
		Plugins: plugin.PluginSet{
			transport.PluginName: &transport.Map{PluginImpl: &BlobServer{}},
		},
		Logger: logger,
	})
}
