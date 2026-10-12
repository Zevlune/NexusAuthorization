package model

import "github.com/google/uuid"

// APIEndpoint 是服务资源作用域内的 HTTP API 端点持久化记录。
type APIEndpoint struct {
	BaseFields
	ID              uuid.UUID
	ServiceResource string
	Controller      string
	Method          string
	PathTemplate    string
	Summary         string
	Enabled         bool
}
