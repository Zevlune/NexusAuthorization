package model

import "time"

// BaseFields contains the audit state shared by every permission record.
type BaseFields struct {
	CreatedByID   string
	CreatedByName string
	CreatedAt     time.Time
	UpdatedByID   string
	UpdatedByName string
	UpdatedAt     time.Time
	IsDeleted     bool
}
