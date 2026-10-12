package repo

import (
	"github.com/luck/permission-center-go/internal/biz"
	"github.com/luck/permission-center-go/internal/data/db/model"
)

func toBizRole(value *model.Role) *biz.Role {
	if value == nil {
		return nil
	}
	result := &biz.Role{
		BaseFields: biz.BaseFields{
			CreatedByID:   value.CreatedByID,
			CreatedByName: value.CreatedByName,
			CreatedAt:     value.CreatedAt,
			UpdatedByID:   value.UpdatedByID,
			UpdatedByName: value.UpdatedByName,
			UpdatedAt:     value.UpdatedAt,
			IsDeleted:     value.IsDeleted,
		},
		ID:          value.ID,
		Code:        value.Code,
		Name:        value.Name,
		Description: value.Description,
		Enabled:     value.Enabled,
	}
	result.ServiceResource = value.ServiceResource
	return result
}

func toBizMenu(value *model.Menu) *biz.Menu {
	if value == nil {
		return nil
	}
	result := &biz.Menu{
		BaseFields: biz.BaseFields{
			CreatedByID:   value.CreatedByID,
			CreatedByName: value.CreatedByName,
			CreatedAt:     value.CreatedAt,
			UpdatedByID:   value.UpdatedByID,
			UpdatedByName: value.UpdatedByName,
			UpdatedAt:     value.UpdatedAt,
			IsDeleted:     value.IsDeleted,
		},
		ID:          value.ID,
		ParentID:    value.ParentID,
		Code:        value.Code,
		Name:        value.Name,
		Description: value.Description,
		Type:        biz.MenuType(value.Type),
		Path:        value.Path,
		Component:   value.Component,
		APIPath:     value.APIPath,
		HTTPMethod:  biz.MenuHTTPMethod(value.HTTPMethod),
		Icon:        value.Icon,
		Sort:        value.Sort,
		Enabled:     value.Enabled,
	}
	result.ServiceResource = value.ServiceResource
	return result
}

func baseFields(value model.BaseFields) biz.BaseFields {
	return biz.BaseFields{
		CreatedByID: value.CreatedByID, CreatedByName: value.CreatedByName,
		CreatedAt: value.CreatedAt, UpdatedByID: value.UpdatedByID,
		UpdatedByName: value.UpdatedByName, UpdatedAt: value.UpdatedAt,
		IsDeleted: value.IsDeleted,
	}
}
