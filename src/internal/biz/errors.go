package biz

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotFound        = errors.New("not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrConflict        = errors.New("conflict")
	// ErrPublishPending reports that a save was committed to the database but
	// its Redis service API records could not be rebuilt yet. The background
	// reconciler retries the publication.
	ErrPublishPending = errors.New("saved, but publishing authorization records is pending")
	// ErrSubjectAPIRecordNotFound reports that an application's Redis records
	// are published and ready, but none exists for the requested subject,
	// domain and service. Authorization treats it as an ordinary deny.
	ErrSubjectAPIRecordNotFound = fmt.Errorf("%w: no service API record for this subject", ErrNotFound)
)
