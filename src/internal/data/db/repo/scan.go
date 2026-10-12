package repo

import "github.com/luck/permission-center-go/internal/data/db/model"

// rowScanner is implemented by both pgx.Row and pgx.Rows. Keeping the scan
// order in one place prevents SELECT/RETURNING projections from drifting.
type rowScanner interface {
	Scan(...any) error
}

func scanRole(row rowScanner) (*model.Role, error) {
	value := &model.Role{}
	err := row.Scan(
		&value.ID,
		&value.ServiceResource,
		&value.Code,
		&value.Name,
		&value.Description,
		&value.Enabled,
		&value.CreatedByID,
		&value.CreatedByName,
		&value.CreatedAt,
		&value.UpdatedByID,
		&value.UpdatedByName,
		&value.UpdatedAt,
		&value.IsDeleted,
	)
	return value, err
}

func scanMenu(row rowScanner) (*model.Menu, error) {
	value := &model.Menu{}
	err := row.Scan(
		&value.ID,
		&value.ServiceResource,
		&value.ParentID,
		&value.Type,
		&value.Code,
		&value.Name,
		&value.Description,
		&value.Path,
		&value.Component,
		&value.APIPath,
		&value.HTTPMethod,
		&value.Icon,
		&value.Sort,
		&value.Metadata,
		&value.Enabled,
		&value.CreatedByID,
		&value.CreatedByName,
		&value.CreatedAt,
		&value.UpdatedByID,
		&value.UpdatedByName,
		&value.UpdatedAt,
		&value.IsDeleted,
	)
	return value, err
}
