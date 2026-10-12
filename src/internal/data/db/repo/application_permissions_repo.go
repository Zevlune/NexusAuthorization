package repo

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/biz"
)

func (r *RBACV1Repository) ListApplicationMenus(ctx context.Context, app string) ([]biz.ApplicationMenu, error) {
	aid, err := r.applicationID(ctx, app)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,application_id,parent_id,code,name,menu_type,route,component,icon,sort_order,enabled FROM menus WHERE application_id=$1 ORDER BY sort_order,id`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []biz.ApplicationMenu{}
	for rows.Next() {
		var v biz.ApplicationMenu
		if err = rows.Scan(&v.ID, &v.ApplicationID, &v.ParentID, &v.Code, &v.Name, &v.MenuType, &v.Route, &v.Component, &v.Icon, &v.SortOrder, &v.Enabled); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (r *RBACV1Repository) SaveApplicationMenu(ctx context.Context, app string, v biz.ApplicationMenu) (biz.ApplicationMenu, error) {
	aid, err := r.applicationID(ctx, app)
	if err != nil {
		return v, err
	}
	v.ApplicationID = aid
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM applications WHERE id=$1 FOR UPDATE`, aid); err != nil {
		return v, err
	}
	if v.ParentID != nil {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM menus WHERE id=$1 AND application_id=$2)`, *v.ParentID, aid).Scan(&valid)
		if err != nil {
			return v, err
		}
		if !valid {
			return v, fmt.Errorf("%w: parent outside application", biz.ErrInvalidArgument)
		}
		var cycle bool
		err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (SELECT id,parent_id FROM menus WHERE id=$1 UNION SELECT m.id,m.parent_id FROM menus m JOIN ancestors a ON m.id=a.parent_id) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$2)`, *v.ParentID, v.ID).Scan(&cycle)
		if err != nil {
			return v, err
		}
		if cycle {
			return v, fmt.Errorf("%w: cyclic menu hierarchy", biz.ErrInvalidArgument)
		}
	}
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO menus(id,application_id,parent_id,code,name,menu_type,route,component,icon,sort_order,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, v.ID, aid, v.ParentID, v.Code, v.Name, v.MenuType, v.Route, v.Component, v.Icon, v.SortOrder, v.Enabled)
	} else {
		var id uuid.UUID
		err = tx.QueryRow(ctx, `UPDATE menus SET parent_id=$3,code=$4,name=$5,menu_type=$6,route=$7,component=$8,icon=$9,sort_order=$10,enabled=$11,updated_at=now() WHERE id=$1 AND application_id=$2 RETURNING id`, v.ID, aid, v.ParentID, v.Code, v.Name, v.MenuType, v.Route, v.Component, v.Icon, v.SortOrder, v.Enabled).Scan(&id)
	}
	if err != nil {
		return v, mapDBError(err)
	}
	return v, tx.Commit(ctx)
}
func (r *RBACV1Repository) DeleteApplicationMenu(ctx context.Context, app string, id uuid.UUID) error {
	aid, err := r.applicationID(ctx, app)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM menus WHERE id=$1 AND application_id=$2`, id, aid)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return biz.ErrNotFound
	}
	return nil
}
