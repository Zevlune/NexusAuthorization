package model

import "github.com/google/uuid"

// RoleMenu records one role-to-menu grant. Menu rows include both menu and
// button nodes, so this relation is also the source of button permissions.
type RoleMenu struct {
	BaseFields
	RoleID string
	MenuID uuid.UUID
}
