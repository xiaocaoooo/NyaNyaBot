package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

type Screenshot struct {
	mu  sync.RWMutex
	cfg struct {
		ScreenshotServer string `json:"screenshot_server"`
	}
}

type buildURLParams struct {
	PageURL     string            `json:"page_url"`
	URL         string            `json:"url"`
	Selector    string            `json:"selector"`
	Width       *int              `json:"width"`
	Height      *int              `json:"height"`
	Format      string            `json:"format"`
	Quality     *int              `json:"quality"`
	WaitTime    *int              `json:"wait_time"`
	WaitFor     string            `json:"wait_for"`
	FullPage    *bool             `json:"full_page"`
	Headers     map[string]string `json:"headers"`
	UserAgent   string            `json:"user_agent"`
	DeviceScale *float64          `json:"device_scale"`
	Mobile      *bool             `json:"mobile"`
	Landscape   *bool             `json:"landscape"`
	Timeout     *int              `json:"timeout"`
	Transparent *bool             `json:"transparent"`
}

func (s *Screenshot) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"screenshot_server":{"type":"string","description":"截图服务域名/地址（例：127.0.0.1:8080）"}
		},
		"additionalProperties":true
	}`)
	def := json.RawMessage(`{"screenshot_server":""}`)

	return papi.Descriptor{
		Name:         "Screenshot",
		PluginID:     "external.screenshot",
		Version:      "0.1.0",
		Author:       "nyanyabot",
		Description:  "Screenshot URL builder plugin",
		Dependencies: []string{},
		Exports: []papi.ExportSpec{
			{
				Name:        "screenshot.build_url",
				Description: "根据 page_url 生成截图服务 URL",
				ParamsSchema: json.RawMessage(`{
					"type":"object",
					"properties":{
						"page_url":{"type":"string","description":"目标页面 URL（优先使用 page_url）"},
						"url":{"type":"string","description":"page_url 的别名"},
						"selector":{"type":"string"},
						"width":{"type":"integer","minimum":100,"maximum":4096},
						"height":{"type":"integer","minimum":0,"maximum":10000},
						"format":{"type":"string","enum":["png","jpeg","webp"]},
						"quality":{"type":"integer","minimum":1,"maximum":100},
						"wait_time":{"type":"integer","minimum":0},
						"wait_for":{"type":"string"},
						"full_page":{"type":"boolean"},
						"headers":{"type":"object","additionalProperties":{"type":"string"}},
						"user_agent":{"type":"string"},
						"device_scale":{"type":"number","exclusiveMinimum":0,"maximum":4},
						"mobile":{"type":"boolean"},
						"landscape":{"type":"boolean"},
						"timeout":{"type":"integer","minimum":1,"maximum":120},
						"transparent":{"type":"boolean","description":"是否使用透明背景"}
					},
					"anyOf":[
						{"required":["page_url"]},
						{"required":["url"]}
					],
					"additionalProperties":false
				}`),
				ResultSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
			},
		},
		Config:   &papi.ConfigSpec{Version: "1", Description: "Screenshot plugin config", Schema: schema, Default: def},
		Commands: []papi.CommandListener{},
		Events:   []papi.EventListener{},
	}, nil
}

func (s *Screenshot) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	parsed := struct {
		ScreenshotServer string `json:"screenshot_server"`
	}{}
	if len(config) > 0 {
		_ = json.Unmarshal(config, &parsed)
	}

	s.mu.Lock()
	s.cfg.ScreenshotServer = strings.TrimSpace(parsed.ScreenshotServer)
	s.mu.Unlock()
	return nil
}

func (s *Screenshot) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	_ = callerPluginID
	if method != "screenshot.build_url" {
		return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "method is not exported")
	}

	var req buildURLParams
	if err := json.Unmarshal(paramsJSON, &req); err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, "params must be a JSON object")
	}
	req.PageURL = strings.TrimSpace(req.PageURL)
	req.URL = strings.TrimSpace(req.URL)
	if req.PageURL == "" {
		req.PageURL = req.URL
	}
	if req.PageURL == "" {
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, "page_url is required")
	}

	s.mu.RLock()
	server := s.cfg.ScreenshotServer
	s.mu.RUnlock()
	if strings.TrimSpace(server) == "" {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, "screenshot_server is not configured")
	}

	built, err := buildScreenshotURL(server, req)
	if err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, err.Error())
	}
	if built == "" {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, "failed to build screenshot url")
	}
	out, err := json.Marshal(map[string]string{"url": built})
	if err != nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, err.Error())
	}
	return out, nil
}

func (s *Screenshot) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	_ = listenerID
	_ = eventRaw
	_ = match
	return papi.HandleResult{}, nil
}

func (s *Screenshot) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func buildScreenshotURL(screenshotServer string, req buildURLParams) (string, error) {
	base := normalizeHTTPBase(screenshotServer)
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/screenshot"
	q := u.Query()

	q.Set("url", req.PageURL)

	if v := strings.TrimSpace(req.Selector); v != "" {
		q.Set("selector", v)
	}
	if req.Width != nil {
		q.Set("width", strconv.Itoa(*req.Width))
	}
	if req.Height != nil {
		q.Set("height", strconv.Itoa(*req.Height))
	}
	if v := strings.TrimSpace(req.Format); v != "" {
		q.Set("format", v)
	}
	if req.Quality != nil {
		q.Set("quality", strconv.Itoa(*req.Quality))
	}
	if req.WaitTime != nil {
		q.Set("wait_time", strconv.Itoa(*req.WaitTime))
	}
	if v := strings.TrimSpace(req.WaitFor); v != "" {
		q.Set("wait_for", v)
	}
	if req.FullPage != nil {
		q.Set("full_page", strconv.FormatBool(*req.FullPage))
	}
	if len(req.Headers) > 0 {
		headersJSON, err := json.Marshal(req.Headers)
		if err != nil {
			return "", fmt.Errorf("headers must be a valid object: %w", err)
		}
		q.Set("headers", string(headersJSON))
	}
	if v := strings.TrimSpace(req.UserAgent); v != "" {
		q.Set("user_agent", v)
	}
	if req.DeviceScale != nil {
		q.Set("device_scale", strconv.FormatFloat(*req.DeviceScale, 'f', -1, 64))
	}
	if req.Mobile != nil {
		q.Set("mobile", strconv.FormatBool(*req.Mobile))
	}
	if req.Landscape != nil {
		q.Set("landscape", strconv.FormatBool(*req.Landscape))
	}
	if req.Timeout != nil {
		q.Set("timeout", strconv.Itoa(*req.Timeout))
	}
	if req.Transparent != nil {
		q.Set("transparent", strconv.FormatBool(*req.Transparent))
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
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
	logger := hclog.New(&hclog.LoggerOptions{Name: "nyanyabot-plugin-screenshot", Level: hclog.Info})

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake(),
		Plugins: plugin.PluginSet{
			transport.PluginName: &transport.Map{PluginImpl: &Screenshot{}},
		},
		Logger: logger,
	})
}
