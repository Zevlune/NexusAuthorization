package repo

import (
	"testing"

	"github.com/luck/permission-center-go/internal/biz"
)

func TestServiceResourceRepositoryImplementsBusinessContract(t *testing.T) {
	var _ biz.ServiceResourceRepository = (*ServiceResourceRepository)(nil)
	var _ biz.ServiceResourceSourceRepository = (*ServiceResourceRepository)(nil)
}
