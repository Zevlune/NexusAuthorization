package repo

// rowScanner is implemented by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(...any) error
}
