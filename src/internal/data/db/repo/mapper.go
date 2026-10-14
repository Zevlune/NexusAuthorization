package repo

import (
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/data/db/model"
)

func baseFields(value model.BaseFields) biz.BaseFields {
	return biz.BaseFields{
		CreatedByID: value.CreatedByID, CreatedByName: value.CreatedByName,
		CreatedAt: value.CreatedAt, UpdatedByID: value.UpdatedByID,
		UpdatedByName: value.UpdatedByName, UpdatedAt: value.UpdatedAt,
		IsDeleted: value.IsDeleted,
	}
}
