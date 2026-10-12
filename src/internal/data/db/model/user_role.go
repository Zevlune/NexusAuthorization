package model

// UserRole is the persistence record for a subject-to-role assignment. The
// 服务资源作用域通过关联角色确定，因此一个用户可安全拥有多个服务资源的角色。
type UserRole struct {
	BaseFields
	Subject string
	RoleID  string
}
