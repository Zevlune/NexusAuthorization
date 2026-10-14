package repo

import "github.com/luck/nexus-authorization/internal/biz"

// Retained for existing menu repository regression tests.
func nullableMenuHTTPMethod(method biz.MenuHTTPMethod) any {
	if method == "" {
		return nil
	}
	return string(method)
}
