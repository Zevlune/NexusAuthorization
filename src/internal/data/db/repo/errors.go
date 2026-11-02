package repo

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/luck/nexus-authorization/internal/biz"
)

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "22001": // string_data_right_truncation
		return biz.ErrInvalidArgument
	case "23505": // unique_violation
		return biz.ErrAlreadyExists
	case "23503", "23514": // foreign_key_violation/check_violation
		return biz.ErrConflict
	default:
		return err
	}
}
