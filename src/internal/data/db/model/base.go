package model

import "time"

// BaseFields contains audit state for the compatibility API endpoint catalog.
type BaseFields struct {
	CreatedByID   string
	CreatedByName string
	CreatedAt     time.Time
	UpdatedByID   string
	UpdatedByName string
	UpdatedAt     time.Time
	IsDeleted     bool
}
