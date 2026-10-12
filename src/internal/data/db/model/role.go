package model

// Role 归属于一个服务资源，是菜单授权和 API 端点授权的主体。
type Role struct {
	BaseFields
	ID              string
	ServiceResource string
	Code            string
	Name            string
	Description     string
	Enabled         bool
}
