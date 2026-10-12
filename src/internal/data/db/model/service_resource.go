package model

// ServiceResource 是权限中心服务资源目录中的持久化记录。
// ResourceKey 是稳定的业务主键，ExternalID 仅保存 NexusAuth 返回的外部标识。
type ServiceResource struct {
	BaseFields
	ResourceKey string
	Source      string
	ExternalID  *string
	DisplayName string
	Audience    string
	Description string
	Enabled     bool
}
