package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// This repository exposes both sources, as the production repository does.
// The selected mode must determine which one is called even if the other
// source contains an allowing policy or returns an error.
type authorizationModeRepository struct {
	subjectAPIRepositoryFixture
	databaseReads int
	databaseError error
}

func (r *authorizationModeRepository) GetPolicyMetadata(context.Context, string) (CurrentPolicy, error) {
	r.databaseReads++
	return r.active, r.databaseError
}

func TestDatabaseAuthorizationUsesCurrentRecordsWithoutRedis(t *testing.T) {
	repository := &authorizationModeRepository{}
	repository.active = CurrentPolicy{ApplicationID: uuid.New(), Token: "granted", Data: directPolicyData(true, true)}
	repository.setError(errors.New("Redis must not be used"))
	service := NewRBACV1Service(repository).WithDatabaseAuthorization()
	input := subjectAPITestInput("user-1", "tenant-a", "order-service", "/books")

	decision, err := service.Authorize(context.Background(), "console", input)
	if err != nil || !decision.Allowed {
		t.Fatalf("database grant = %+v, %v", decision, err)
	}

	// A save changes the database content digest. A cached allowing decision
	// must not survive revocation in database mode.
	repository.active.Token = "revoked"
	repository.active.Data = directPolicyData(true, false)
	decision, err = service.Authorize(context.Background(), "console", input)
	if err != nil || decision.Allowed || decision.Reason != "endpoint_not_granted" {
		t.Fatalf("database revocation = %+v, %v", decision, err)
	}
	if repository.readCount() != 0 || repository.databaseReads != 2 {
		t.Fatalf("source reads: Redis=%d database=%d", repository.readCount(), repository.databaseReads)
	}
}

func TestRedisAuthorizationDoesNotFallBackToDatabase(t *testing.T) {
	repository := &authorizationModeRepository{}
	repository.active = CurrentPolicy{Data: directPolicyData(true, true)}
	redisError := errors.New("Redis unavailable")
	repository.setError(redisError)

	decision, err := NewRBACV1Service(repository).Authorize(context.Background(), "console", subjectAPITestInput("user-1", "tenant-a", "order-service", "/books"))
	if decision.Allowed || !errors.Is(err, redisError) {
		t.Fatalf("Redis failure = %+v, %v", decision, err)
	}
	if repository.readCount() != 1 || repository.databaseReads != 0 {
		t.Fatalf("source reads: Redis=%d database=%d", repository.readCount(), repository.databaseReads)
	}
}

func TestDatabaseAuthorizationFailsClosedOnDatabaseError(t *testing.T) {
	repository := &authorizationModeRepository{}
	repository.active = CurrentPolicy{Data: directPolicyData(true, true)}
	databaseError := errors.New("database unavailable")
	repository.databaseError = databaseError

	decision, err := NewRBACV1Service(repository).WithDatabaseAuthorization().Authorize(context.Background(), "console", subjectAPITestInput("user-1", "tenant-a", "order-service", "/books"))
	if decision.Allowed || !errors.Is(err, databaseError) {
		t.Fatalf("database failure = %+v, %v", decision, err)
	}
	if repository.readCount() != 0 {
		t.Fatalf("database mode read Redis %d times", repository.readCount())
	}
}
