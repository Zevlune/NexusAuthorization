import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Dialog, Drawer, Empty, EnhancedTable, Form, Input, Loading, MessagePlugin, Select, Space, Tag, type TableProps } from 'tdesign-react';

import {
  createMenu,
  deleteMenu,
  getMenuTree,
  updateMenu,
  type CreateMenuRequest,
  type Menu,
  type MenuHTTPMethod,
  type MenuType,
  type UpdateMenuRequest,
} from '../../../api/permission';
import { getRequestErrorMessage, PageHeader, useServiceResourceScope } from '../shared';
import '../style.less';

type MenuForm = {
  type: MenuType;
  parentId: string;
  code: string;
  name: string;
  description: string;
  path: string;
  component: string;
  apiPath: string;
  httpMethod: MenuHTTPMethod;
  icon: string;
  sort: string;
  enabled: boolean;
};

const EMPTY_MENU_FORM: MenuForm = {
  type: 'menu',
  parentId: '',
  code: '',
  name: '',
  description: '',
  path: '',
  component: '',
  apiPath: '',
  httpMethod: 'GET',
  icon: '',
  sort: '0',
  enabled: true,
};

const menuTypeOptions = [
  { label: '菜单', value: 'menu' },
  { label: '按钮', value: 'button' },
];

const httpMethods: MenuHTTPMethod[] = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];
const httpMethodOptions = httpMethods.map((method) => ({ label: method, value: method }));

const statusOptions = [
  { label: '启用', value: 'enabled' },
  { label: '停用', value: 'disabled' },
];

type MenuEditorState = {
  visible: boolean;
  editingMenu: Menu | null;
  form: MenuForm;
};

const flattenMenus = (nodes: Menu[]): Menu[] => nodes.flatMap((node) => [node, ...flattenMenus(node.children)]);

const replaceMenuNode = (nodes: Menu[], updated: Menu): Menu[] => nodes.map((node) => (
  node.id === updated.id
    ? { ...updated, children: node.children }
    : { ...node, children: replaceMenuNode(node.children, updated) }
));

const appendMenuNode = (nodes: Menu[], created: Menu): Menu[] => {
  if (!created.parentId) {
    return [...nodes, created];
  }
  return nodes.map((node) => node.id === created.parentId
    ? { ...node, children: [...node.children, created] }
    : { ...node, children: appendMenuNode(node.children, created) });
};

const removeMenuNode = (nodes: Menu[], menuID: string): Menu[] => nodes
  .filter((node) => node.id !== menuID)
  .map((node) => ({ ...node, children: removeMenuNode(node.children, menuID) }));

const MenuManagementPage = () => {
  const { serviceResource } = useServiceResourceScope();
  const [tree, setTree] = useState<Menu[]>([]);
  const [loading, setLoading] = useState(false);
  const [menuEditor, setMenuEditor] = useState<MenuEditorState>({
    visible: false,
    editingMenu: null,
    form: { ...EMPTY_MENU_FORM },
  });
  const [savingMenu, setSavingMenu] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Menu | null>(null);
  const [deleting, setDeleting] = useState(false);
  const { editingMenu, form: menuForm, visible: drawerVisible } = menuEditor;

  const loadTree = useCallback(async (scope: string) => {
    setLoading(true);
    try {
      setTree(await getMenuTree(scope));
    } catch (error) {
      setTree([]);
      MessagePlugin.error(getRequestErrorMessage(error, '加载菜单树失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadTree(serviceResource);
  }, [serviceResource, loadTree]);

  const menuParents = useMemo(
    () => flattenMenus(tree)
      .filter((menu) => menu.type === 'menu' && menu.enabled)
      .map((menu) => ({ label: menu.name || menu.code, value: menu.id })),
    [tree],
  );

  const resetMenuEditor = () => {
    setMenuEditor((current) => ({
      ...current,
      visible: false,
      editingMenu: null,
      form: { ...EMPTY_MENU_FORM },
    }));
  };

  const openCreateDrawer = () => {
    setMenuEditor((current) => ({
      ...current,
      visible: true,
      editingMenu: null,
      form: { ...EMPTY_MENU_FORM },
    }));
  };

  const openEditDrawer = (menu: Menu) => {
    setMenuEditor((current) => ({
      ...current,
      visible: true,
      editingMenu: menu,
      form: {
        type: menu.type,
        parentId: menu.parentId || '',
        code: menu.code,
        name: menu.name,
        description: menu.description,
        path: menu.path,
        component: menu.component,
        apiPath: menu.apiPath,
        httpMethod: menu.httpMethod || 'GET',
        icon: menu.icon,
        sort: String(menu.sort),
        enabled: menu.enabled,
      },
    }));
  };

  const updateMenuForm = (update: (previous: MenuForm) => MenuForm) => {
    setMenuEditor((current) => ({
      ...current,
      form: update(current.form),
    }));
  };

  const closeMenuEditor = () => {
    if (savingMenu) {
      return;
    }
    resetMenuEditor();
  };

  const submitMenu = async () => {
    const code = menuForm.code.trim();
    const name = menuForm.name.trim();
    const apiPath = menuForm.apiPath.trim();
    if (!code || !name) {
      MessagePlugin.warning('请填写编码和名称');
      return;
    }
    if (menuForm.type === 'button' && !menuForm.parentId) {
      MessagePlugin.warning('按钮必须选择父级菜单');
      return;
    }
    if (menuForm.type === 'button' && !apiPath) {
      MessagePlugin.warning('按钮必须填写 API 路径');
      return;
    }
    const parsedSort = Number(menuForm.sort);
    const sort = Number.isFinite(parsedSort) ? parsedSort : 0;
    const editing = editingMenu;

    setSavingMenu(true);
    try {
      if (editing) {
        const payload: UpdateMenuRequest = {
          code,
          name,
          description: menuForm.description.trim(),
          path: menuForm.path.trim(),
          component: menuForm.component.trim(),
          api_path: editing.type === 'button' ? apiPath : '',
          http_method: editing.type === 'button' ? menuForm.httpMethod : '',
          icon: menuForm.icon.trim(),
          sort,
          enabled: menuForm.enabled,
        };
        const updated = await updateMenu(editing.id, payload);
        setTree((current) => replaceMenuNode(current, updated));
      } else {
        const payload: CreateMenuRequest = {
          service_resource: serviceResource,
          parent_id: menuForm.parentId || null,
          code,
          name,
          description: menuForm.description.trim(),
          type: menuForm.type,
          path: menuForm.type === 'menu' ? menuForm.path.trim() : '',
          component: menuForm.type === 'menu' ? menuForm.component.trim() : '',
          api_path: menuForm.type === 'button' ? apiPath : '',
          http_method: menuForm.type === 'button' ? menuForm.httpMethod : '',
          icon: menuForm.type === 'menu' ? menuForm.icon.trim() : '',
          sort,
        };
        const created = await createMenu(payload);
        setTree((current) => appendMenuNode(current, created));
      }
      resetMenuEditor();
      MessagePlugin.success(editing ? '权限节点已更新' : `${menuForm.type === 'menu' ? '菜单' : '按钮'}已创建`);
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, editing ? '更新权限节点失败' : '创建权限节点失败'));
    } finally {
      setSavingMenu(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) {
      return;
    }
    const target = deleteTarget;
    setDeleting(true);
    try {
      await deleteMenu(target.id);
      setTree((current) => removeMenuNode(current, target.id));
      setDeleteTarget(null);
      MessagePlugin.success('权限节点已删除');
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, '删除权限节点失败'));
    } finally {
      setDeleting(false);
    }
  };

  const columns: TableProps<Menu>['columns'] = [
    {
      colKey: 'name',
      title: '名称',
      minWidth: 260,
      cell: ({ row }) => (
        <Space size="small">
          <Tag theme={row.type === 'menu' ? 'primary' : 'warning'} variant="light-outline">
            {row.type === 'menu' ? '菜单' : '按钮'}
          </Tag>
          <strong title={row.name}>{row.name || '-'}</strong>
          <Tag theme={row.enabled ? 'success' : 'default'} variant="light-outline">
            {row.enabled ? '启用' : '停用'}
          </Tag>
        </Space>
      ),
    },
    { colKey: 'code', title: '编码', minWidth: 180, className: 'permission-table-code', cell: ({ row }) => row.code || '-' },
    {
      colKey: 'path',
      title: '路由 / API',
      minWidth: 220,
      className: 'permission-table-code',
      cell: ({ row }) => row.type === 'button' ? `${row.httpMethod || 'ANY'} ${row.apiPath || '-'}` : row.path || '-',
    },
    {
      colKey: 'component',
      title: '组件 / 描述',
      minWidth: 200,
      className: 'permission-table-muted',
      cell: ({ row }) => row.type === 'menu' ? row.component || '-' : row.description || '-',
    },
    {
      colKey: 'actions',
      title: '操作',
      width: 140,
      cell: ({ row }) => (
        <Space size="small">
          <Button variant="text" theme="primary" type="button" onClick={() => openEditDrawer(row)}>编辑</Button>
          <Button variant="text" theme="danger" type="button" onClick={() => setDeleteTarget(row)}>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="permission-page permission-menu-page">
      <PageHeader
        title="菜单与按钮管理"
        actions={<Button theme="primary" type="button" onClick={openCreateDrawer}>新建权限节点</Button>}
      />

      <Card className="permission-card" bordered>
        <div className="permission-tree">
          <EnhancedTable<Menu>
            className="permission-table permission-menu-table"
            rowKey="id"
            data={loading ? [] : tree}
            columns={columns}
            tree={{ defaultExpandAll: true }}
            loading={loading ? <Loading text="正在加载菜单树..." /> : false}
            hover
            tableLayout="auto"
            empty={<Empty description="当前服务资源暂无菜单或按钮" />}
          />
        </div>
      </Card>

      {drawerVisible ? (
        <Drawer
          key={editingMenu?.id || 'create'}
          visible
          header={editingMenu ? `编辑${menuForm.type === 'menu' ? '菜单' : '按钮'}` : `新建${menuForm.type === 'menu' ? '菜单' : '按钮'}`}
          size="560px"
          confirmBtn={{ content: editingMenu ? '保存' : '创建', theme: 'primary', loading: savingMenu }}
          cancelBtn="取消"
          onConfirm={() => void submitMenu()}
          onCancel={closeMenuEditor}
          onClose={closeMenuEditor}
        >
          <Form className="permission-drawer-form" labelAlign="top" initialData={{ ...menuForm, enabled: menuForm.enabled ? 'enabled' : 'disabled' }}>
          <Form.FormItem label="类型" name="type">
            <Select
              value={menuForm.type}
              options={menuTypeOptions}
              disabled={Boolean(editingMenu)}
              onChange={(value) => updateMenuForm((prev) => ({ ...prev, type: value === 'button' ? 'button' : 'menu' }))}
            />
          </Form.FormItem>
          <Form.FormItem label="父级菜单" name="parentId" help="菜单可作为根节点或挂在已有菜单下，按钮必须挂在菜单下。">
            <Select
              value={menuForm.parentId}
              options={[{ label: '根节点', value: '' }, ...menuParents]}
              placeholder="请选择父级菜单"
              filterable
              disabled={Boolean(editingMenu)}
              onChange={(value) => updateMenuForm((prev) => ({ ...prev, parentId: String(value || '') }))}
            />
          </Form.FormItem>
          <Form.FormItem label="编码" name="code">
            <Input value={menuForm.code} maxlength={100} placeholder="例如 article-management" onChange={(value) => updateMenuForm((prev) => ({ ...prev, code: value }))} />
          </Form.FormItem>
          <Form.FormItem label="名称" name="name">
            <Input value={menuForm.name} maxlength={100} placeholder="例如 文章管理" onChange={(value) => updateMenuForm((prev) => ({ ...prev, name: value }))} />
          </Form.FormItem>
          <Form.FormItem label="排序" name="sort">
            <Input type="number" value={menuForm.sort} placeholder="0" onChange={(value) => updateMenuForm((prev) => ({ ...prev, sort: value }))} />
          </Form.FormItem>
          <Form.FormItem label="描述" name="description">
            <Input value={menuForm.description} maxlength={200} placeholder="可选" onChange={(value) => updateMenuForm((prev) => ({ ...prev, description: value }))} />
          </Form.FormItem>

          {menuForm.type === 'menu' ? (
            <>
              <Form.FormItem label="路由路径" name="path">
                <Input value={menuForm.path} placeholder="例如 /articles" onChange={(value) => updateMenuForm((prev) => ({ ...prev, path: value }))} />
              </Form.FormItem>
              <Form.FormItem label="组件路径" name="component">
                <Input value={menuForm.component} placeholder="例如 /pages/articles" onChange={(value) => updateMenuForm((prev) => ({ ...prev, component: value }))} />
              </Form.FormItem>
              <Form.FormItem label="图标标识" name="icon">
                <Input value={menuForm.icon} placeholder="可选" onChange={(value) => updateMenuForm((prev) => ({ ...prev, icon: value }))} />
              </Form.FormItem>
            </>
          ) : (
            <>
              <Form.FormItem label="API 路径" name="apiPath" className="full-width">
                <Input value={menuForm.apiPath} placeholder="例如 /v1/articles" onChange={(value) => updateMenuForm((prev) => ({ ...prev, apiPath: value }))} />
              </Form.FormItem>
              <Form.FormItem label="HTTP 方法" name="httpMethod">
                <Select value={menuForm.httpMethod} options={httpMethodOptions} onChange={(value) => updateMenuForm((prev) => ({ ...prev, httpMethod: value as MenuHTTPMethod }))} />
              </Form.FormItem>
            </>
          )}
          {editingMenu ? (
            <Form.FormItem
              label="启用状态"
              name="enabled"
            >
              <Select
                value={menuForm.enabled ? 'enabled' : 'disabled'}
                options={statusOptions}
                onChange={(value) => updateMenuForm((prev) => ({ ...prev, enabled: value === 'enabled' }))}
              />
            </Form.FormItem>
          ) : null}
          </Form>
        </Drawer>
      ) : null}

      <Dialog
        visible={Boolean(deleteTarget)}
        header="删除权限节点"
        width={460}
        confirmBtn={{ content: '确认删除', theme: 'danger', loading: deleting }}
        cancelBtn="取消"
        confirmLoading={deleting}
        onConfirm={() => void confirmDelete()}
        onCancel={() => { if (!deleting) setDeleteTarget(null); }}
        onClose={() => { if (!deleting) setDeleteTarget(null); }}
        destroyOnClose
      >
        <p className="permission-delete-warning">
          确认删除 <strong>{deleteTarget?.name || deleteTarget?.code || ''}</strong>？如果该节点有子节点，删除可能会被服务端拒绝。
        </p>
      </Dialog>
    </div>
  );
};

export default MenuManagementPage;
