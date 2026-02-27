package ob11

import "encoding/json"

// Event is the raw OneBot 11 / NapCat event payload.
// We keep it untyped to stay forward-compatible.
type Event = json.RawMessage

// APIRequest is a OneBot API call over websocket.
// NapCat supports go-cqhttp compatible schema.
type APIRequest struct {
	Action string      `json:"action"`
	Params interface{} `json:"params,omitempty"`
	Echo   string      `json:"echo,omitempty"`
}

// APIResponse is the response for an APIRequest.
type APIResponse struct {
	Status  string          `json:"status"`
	RetCode int             `json:"retcode"`
	Msg     string          `json:"msg,omitempty"`
	Wording string          `json:"wording,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Echo    string          `json:"echo,omitempty"`
}
