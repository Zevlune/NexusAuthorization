package model

import "github.com/google/uuid"

type MenuType string

const (
	MenuTypeMenu   MenuType = "menu"
	MenuTypeButton MenuType = "button"
)

// Menu 是服务资源菜单树中的菜单或按钮节点。
type Menu struct {
	BaseFields
	ID              uuid.UUID
	ServiceResource string
	ParentID        *uuid.UUID
	Code            string
	Name            string
	Description     string
	Type            MenuType
	Path            string
	Component       string
	APIPath         string
	HTTPMethod      string
	Icon            string
	Sort            int
	Metadata        []byte
	Enabled         bool
}
