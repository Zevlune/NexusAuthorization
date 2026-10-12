package biz

import (
	"context"
	"strings"
	"time"
)

// BaseFields 是所有权限记录共用的审计字段投影。
// 审计身份由已认证请求上下文提供，不从请求 JSON 获取。
type BaseFields struct {
	CreatedByID   string    `json:"created_by_id"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedByID   string    `json:"updated_by_id"`
	UpdatedByName string    `json:"updated_by_name"`
	UpdatedAt     time.Time `json:"updated_at"`
	IsDeleted     bool      `json:"is_deleted"`
}

// AuditActor 表示写入权限记录审计字段的操作者身份。
type AuditActor struct {
	ID   string
	Name string
}

type auditActorContextKey struct{}

// WithAuditActor 将审计操作者写入上下文；缺少 ID 或名称时分别使用 system 或 ID 作为默认值。
func WithAuditActor(ctx context.Context, actor AuditActor) context.Context {
	actor.ID = strings.TrimSpace(actor.ID)
	actor.Name = strings.TrimSpace(actor.Name)
	if actor.ID == "" {
		actor.ID = "system"
	}
	if actor.Name == "" {
		actor.Name = actor.ID
	}
	return context.WithValue(ctx, auditActorContextKey{}, actor)
}

// AuditActorFromContext 从上下文读取审计操作者；未配置时返回 system。
func AuditActorFromContext(ctx context.Context) AuditActor {
	if ctx != nil {
		if actor, ok := ctx.Value(auditActorContextKey{}).(AuditActor); ok && strings.TrimSpace(actor.ID) != "" {
			actor.ID = strings.TrimSpace(actor.ID)
			actor.Name = strings.TrimSpace(actor.Name)
			if actor.Name == "" {
				actor.Name = actor.ID
			}
			return actor
		}
	}
	return AuditActor{ID: "system", Name: "system"}
}

// NewBaseFields 根据操作者初始化创建和更新审计字段。
func NewBaseFields(actor AuditActor) BaseFields {
	if strings.TrimSpace(actor.ID) == "" {
		actor = AuditActor{ID: "system", Name: "system"}
	}
	if strings.TrimSpace(actor.Name) == "" {
		actor.Name = actor.ID
	}
	return BaseFields{
		CreatedByID:   actor.ID,
		CreatedByName: actor.Name,
		UpdatedByID:   actor.ID,
		UpdatedByName: actor.Name,
	}
}
