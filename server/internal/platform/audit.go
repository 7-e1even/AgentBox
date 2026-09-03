package platform

import "context"

// AuditActor is authenticated identity, never a credential or request payload.
type AuditActor struct {
	Type string
	ID   string
	Name string
	Role UserRole
}

type auditActorKey struct{}

func WithAuditActor(ctx context.Context, actor AuditActor) context.Context {
	return context.WithValue(ctx, auditActorKey{}, actor)
}

func AuditActorFromContext(ctx context.Context) AuditActor {
	actor, _ := ctx.Value(auditActorKey{}).(AuditActor)
	return actor
}
