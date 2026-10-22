package biz

import (
	"context"
	"strings"
)

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
