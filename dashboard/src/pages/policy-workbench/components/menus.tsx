import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Alert, Button, Dialog, Drawer, EnhancedTable, Form, Input, MessagePlugin, Radio, Select, Space, Switch, Tag } from 'tdesign-react';
import type { TableProps } from 'tdesign-react/es/table/interface';
import { AddIcon, DeleteIcon, EditIcon, SearchIcon, RefreshIcon } from 'tdesign-icons-react';
import { deleteMenu, saveMenu, type Menu } from '../../../api/rbac-v1';
import { Empty, fail, Guard, Page, type State } from '../shared';
import './menus.less';

type MenuDraft = {
  id?: string;
  parent_id: string | null;
  code: string;
  name: string;
  menu_type: Menu['menu_type'];
  route: string;
  component: string;
  icon: string;
  sort_order: number;
  enabled: boolean;
};

type MenuNode = Menu & { children: MenuNode[] };
type MenuTableRow = MenuNode & Record<string, unknown>;

const EMPTY_DRAFT: MenuDraft = {
  parent_id: null,
  code: '',
  name: '',
  menu_type: 'menu',
  route: '',
  component: '',
  icon: '',
  sort_order: 0,
  enabled: true,
};

const typeLabel: Record<Menu['menu_type'], string> = { directory: '目录', menu: '菜单', button: '按钮' };

const MenuField = ({ label, help, children }: { label: string; help?: string; children: ReactNode }) => (
  <Form.FormItem label={label} help={help}>{children}</Form.FormItem>
);

const sortNodes = (nodes: MenuNode[]): MenuNode[] => [...nodes].sort((left, right) => left.sort_order - right.sort_order || left.name.localeCompare(right.name));

/** Build a bounded tree from API data. Invalid or cyclic parent links become roots. */
const buildTree = (items: Menu[]): MenuNode[] => {
  const nodes = new Map<string, MenuNode>();
  items.forEach((item) => nodes.set(item.id, { ...item, children: [] }));
  const roots: MenuNode[] = [];
  const hasCycle = (id: string, parentId: string): boolean => {
    const seen = new Set<string>();
    let current: string | null = parentId;
    while (current) {
      if (current === id) return true;
      if (seen.has(current)) return true;
      seen.add(current);
      current = nodes.get(current)?.parent_id || null;
    }
    return false;
  };
  nodes.forEach((node) => {
    const parent = node.parent_id ? nodes.get(node.parent_id) : undefined;
    if (!parent || parent.menu_type === 'button' || hasCycle(node.id, parent.id)) {
      roots.push(node);
    } else {
      parent.children.push(node);
    }
  });
  const normalize = (node: MenuNode): MenuNode => ({ ...node, children: sortNodes(node.children).map(normalize) });
  return sortNodes(roots).map(normalize);
};

const flattenNodes = (nodes: MenuNode[]): MenuNode[] => nodes.flatMap((node) => [node, ...flattenNodes(node.children)]);

const makeDraft = (menu?: Menu, kind: Menu['menu_type'] = 'menu', parentId: string | null = null): MenuDraft => ({
  ...EMPTY_DRAFT,
  ...(menu || {}),
  id: menu?.id,
  menu_type: menu?.menu_type || kind,
  parent_id: menu?.parent_id ?? parentId,
  sort_order: menu?.sort_order ?? 0,
  enabled: menu?.enabled ?? true,
  route: menu?.route || '',
  component: menu?.component || '',
  icon: menu?.icon || '',
  code: menu?.code || '',
  name: menu?.name || '',
});

const Menus = ({ state }: { state: State }) => {
  const tree = useMemo(() => buildTree(state.menus), [state.menus]);
  const allNodes = useMemo(() => flattenNodes(tree), [tree]);
  const [query, setQuery] = useState('');
  const [draftQuery, setDraftQuery] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(tree.filter((node) => node.children.length).map((node) => node.id)));
  const [draft, setDraft] = useState<MenuDraft | null>(null);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<MenuNode | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  useEffect(() => {
    setExpanded((current) => {
      const next = new Set(current);
      tree.forEach((node) => { if (node.children.length) next.add(node.id); });
      return next;
    });
  }, [tree]);

  const search = query.trim().toLowerCase();
  const matches = useMemo(() => new Set(allNodes.filter((item) => `${item.name} ${item.code}`.toLowerCase().includes(search)).map((item) => item.id)), [allNodes, search]);
  const visibleTree = useMemo(() => {
    if (!search) return tree as MenuTableRow[];
    const filter = (nodes: MenuNode[]): MenuTableRow[] => nodes.flatMap((node) => {
      const matching = matches.has(node.id);
      const children = matching ? node.children : filter(node.children);
      return matching || children.length ? [{ ...node, children: children as MenuNode[] } as MenuTableRow] : [];
    });
    return filter(tree);
  }, [matches, search, tree]);
  const expandedTreeKeys = search
    ? allNodes.filter((node) => node.children.length).map((node) => node.id)
    : Array.from(expanded);

  const menuColumns: TableProps<MenuTableRow>['columns'] = [
    { colKey: 'name', title: '名称', minWidth: 260, cell: ({ row }) => <strong title={row.name}>{row.name || '-'}</strong> },
    { colKey: 'menu_type', title: '类型', cell: ({ row }) => <Tag theme={row.menu_type === 'button' ? 'warning' : row.menu_type === 'directory' ? 'default' : 'primary'} variant="light-outline">{typeLabel[row.menu_type]}</Tag> },
    { colKey: 'code', title: '权限标识', cell: ({ row }) => <code>{row.code || '-'}</code> },
    { colKey: 'route', title: '路由', cell: ({ row }) => <span title={row.route}>{row.route || '-'}</span> },
    { colKey: 'sort_order', title: '排序' },
    { colKey: 'enabled', title: '状态', cell: ({ row }) => <Tag theme={row.enabled ? 'success' : 'default'} variant="light">{row.enabled ? '启用' : '停用'}</Tag> },
    {
      colKey: 'operation',
      title: '操作',
      width: 310,
      cell: ({ row }) => <Space size="small"><Button variant="text" icon={<EditIcon />} aria-label={`编辑${row.name}`} onClick={() => openEdit(row)}>编辑</Button>{row.menu_type !== 'button' && <><Button variant="text" icon={<AddIcon />} onClick={() => openCreate('menu', row.id)}>添加子菜单</Button><Button variant="text" icon={<AddIcon />} onClick={() => openCreate('button', row.menu_type === 'menu' ? row.id : null)} disabled={row.menu_type !== 'menu'}>添加按钮</Button></>}<Button variant="text" theme="danger" icon={<DeleteIcon />} aria-label={`删除${row.name}`} onClick={() => requestDelete(row)}>删除</Button></Space>,
    },
  ];

  const descendants = useCallback((id: string): Set<string> => {
    const found = new Set<string>();
    const visit = (nodes: MenuNode[]) => nodes.forEach((node) => {
      found.add(node.id);
      visit(node.children);
    });
    const node = allNodes.find((item) => item.id === id);
    visit(node?.children || []);
    return found;
  }, [allNodes]);

  const parentOptions = useMemo(() => {
    const excluded = draft?.id ? new Set([draft.id, ...descendants(draft.id)]) : new Set<string>();
    const allowed = allNodes.filter((item) => !excluded.has(item.id) && (draft?.menu_type === 'button' ? item.menu_type === 'menu' : item.menu_type !== 'button'));
    const options = allowed.map((item) => ({ label: `${item.name || item.code} · ${typeLabel[item.menu_type]}`, value: item.id }));
    return draft?.menu_type === 'button' ? options : [{ label: '根节点', value: '' }, ...options];
  }, [allNodes, descendants, draft]);

  const patchDraft = (update: Partial<MenuDraft>) => setDraft((current) => current ? { ...current, ...update } : current);

  const openCreate = (kind: 'menu' | 'button', parentId: string | null = null) => {
    setFormError('');
    setDraft(makeDraft(undefined, kind, parentId));
  };

  const openEdit = (menu: MenuNode) => {
    setFormError('');
    setDraft(makeDraft(menu));
  };

  const closeDrawer = () => {
    if (!saving) {
      setDraft(null);
      setFormError('');
    }
  };

  const save = async () => {
    if (!draft || saving) return;
    const code = draft.code.trim();
    const name = draft.name.trim();
    const parsedSort = Number(draft.sort_order);
    const parent = draft.parent_id ? allNodes.find((item) => item.id === draft.parent_id) : undefined;
    let error = '';
    if (!name) error = '请填写名称';
    else if (!code) error = '请填写权限标识';
    else if (!Number.isInteger(parsedSort)) error = '排序必须是整数';
    else if (draft.menu_type === 'button' && (!parent || parent.menu_type !== 'menu')) error = '按钮必须选择所属菜单';
    else if (draft.menu_type !== 'button' && parent?.menu_type === 'button') error = '菜单不能挂在按钮下';
    else if (draft.menu_type !== 'button' && draft.route.trim() && !draft.route.trim().startsWith('/')) error = '菜单路由必须以 / 开头';
    else if (draft.id && draft.parent_id === draft.id) error = '上级菜单不能选择当前节点';
    else if (draft.id && draft.parent_id && descendants(draft.id).has(draft.parent_id)) error = '上级菜单不能选择当前节点或其后代';
    if (error) {
      setFormError(error);
      return;
    }
    setSaving(true);
    setFormError('');
    try {
      await saveMenu(state.appKey, {
        ...(draft.id ? { id: draft.id } : {}),
        code,
        name,
        menu_type: draft.menu_type,
        parent_id: draft.parent_id || null,
        route: draft.menu_type === 'button' ? '' : draft.route.trim(),
        component: draft.menu_type === 'button' ? '' : draft.component.trim(),
        icon: draft.menu_type === 'button' ? '' : draft.icon.trim(),
        sort_order: parsedSort,
        enabled: draft.enabled,
      });
      if (draft.parent_id) {
        setExpanded((current) => {
          const next = new Set(current);
          const seen = new Set<string>();
          let parentId: string | null = draft.parent_id;
          while (parentId && !seen.has(parentId)) {
            next.add(parentId);
            seen.add(parentId);
            parentId = allNodes.find((item) => item.id === parentId)?.parent_id || null;
          }
          return next;
        });
      }
      setDraft(null);
      setFormError('');
      await state.reload();
      MessagePlugin.success(draft.id ? '菜单已更新' : `${draft.menu_type === 'button' ? '按钮' : '菜单'}已创建`);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : '保存失败，请稍后重试');
      fail(error, '保存菜单失败');
    } finally {
      setSaving(false);
    }
  };

  const requestDelete = (node: MenuNode) => {
    setDeleteError(node.children.length ? '该节点仍有子项，请先处理子项后再删除。' : '');
    setDeleteTarget(node);
  };

  const confirmDelete = async () => {
    if (!deleteTarget || deleting) return;
    if (deleteTarget.children.length) {
      setDeleteError('该节点仍有子项，请先处理子项后再删除。');
      return;
    }
    setDeleting(true);
    try {
      await deleteMenu(state.appKey, deleteTarget.id);
      setDeleteTarget(null);
      await state.reload();
      MessagePlugin.success('已删除');
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : '删除失败，请稍后重试');
      fail(error, '删除菜单失败');
    } finally {
      setDeleting(false);
    }
  };

  const drawerType = draft?.menu_type || 'menu';
  return <div className="resource-menus">
    <Guard state={state}>
      <Page title="菜单与按钮" actions={<Space><Button variant="outline" icon={<AddIcon />} onClick={() => openCreate('button')}>新增按钮</Button><Button theme="primary" icon={<AddIcon />} onClick={() => openCreate('menu')}>新增菜单</Button></Space>}>
        <div className="page-filter-bar">
          <Form className="workbench-filter-form" layout="inline" colon={false} labelWidth={100} onSubmit={() => setQuery(draftQuery)}>
            <Form.FormItem label="关键词"><Input className="workbench-keyword" value={draftQuery} clearable prefixIcon={<SearchIcon />} placeholder="名称或权限标识" onChange={setDraftQuery} /></Form.FormItem>
            <Form.FormItem><Space size={16}><Button theme="primary" type="submit" icon={<SearchIcon />}>查询</Button><Button theme="primary" icon={<RefreshIcon />} onClick={() => { setDraftQuery(''); setQuery(''); }}>重置</Button></Space></Form.FormItem>
          </Form>
        </div>
        <Space className="workbench-table-actions" size={8}><Button variant="outline" onClick={() => setExpanded(new Set(allNodes.filter((item) => item.children.length).map((item) => item.id)))}>展开全部</Button><Button variant="outline" onClick={() => setExpanded(new Set())}>收起全部</Button></Space>
        <section className="resource-menus-panel" aria-label="菜单与按钮列表">
          <div className="resource-menus-table-wrap">
            <EnhancedTable<MenuTableRow>
              className="resource-menus-table"
              rowKey="id"
              columns={menuColumns}
              data={visibleTree}
              tree={{ indent: 22 }}
              expandedTreeNodes={expandedTreeKeys}
              onExpandedTreeNodesChange={(keys) => { if (!search) setExpanded(new Set(keys.map(String))); }}
              loading={state.loading}
              empty={state.menus.length ? <Empty text="没有匹配的菜单或按钮" /> : <Empty text="尚无菜单或按钮" />}
              hover
              size="small"
            />
          </div>
        </section>
      </Page>
    </Guard>
    <Drawer className="resource-menus-drawer" visible={Boolean(draft)} header={draft?.id ? `编辑${typeLabel[drawerType]}` : `新增${drawerType === 'button' ? '按钮' : '菜单'}`} size="560px" onClose={closeDrawer} footer={<Space className="resource-menus-drawer-footer"><Button variant="outline" disabled={saving} onClick={closeDrawer}>取消</Button><Button theme="primary" loading={saving} disabled={saving} onClick={() => void save()}>保存</Button></Space>}>
      {draft && <Form className="resource-menus-form" labelAlign="top">
        {formError && <Alert className="resource-menus-form-error" theme="error" message={formError} />}
        <MenuField label="类型"><Radio.Group className="resource-menus-type-radio" variant="outline" value={drawerType} disabled={saving || Boolean(draft.id && drawerType === 'directory')} onChange={(value) => { const menuType = value as Menu['menu_type']; patchDraft({ menu_type: menuType, route: menuType === 'button' ? '' : draft.route, component: menuType === 'button' ? '' : draft.component, icon: menuType === 'button' ? '' : draft.icon }); }}><Radio.Button value="menu">菜单</Radio.Button><Radio.Button value="button" disabled={Boolean(draft.id && drawerType !== 'button' && allNodes.find((item) => item.id === draft.id)?.children.length)}>按钮</Radio.Button>{draft.id && drawerType === 'directory' && <Radio.Button value="directory">目录</Radio.Button>}</Radio.Group></MenuField>
        <MenuField label="名称" help={drawerType === 'button' ? '用于页面操作权限的显示名称。' : '用于菜单树和导航中的显示名称。'}><Input value={draft.name} disabled={saving} placeholder={drawerType === 'button' ? '例如：创建订单' : '例如：订单管理'} onChange={(value) => patchDraft({ name: value })} /></MenuField>
        <MenuField label="权限标识" help="用于角色授权，编辑时保持不变。"><Input value={draft.code} disabled={saving || Boolean(draft.id)} placeholder="例如 order:view" onChange={(value) => patchDraft({ code: value })} /></MenuField>
        <MenuField label={drawerType === 'button' ? '所属菜单' : '上级菜单'}><Select value={draft.parent_id || ''} options={parentOptions} placeholder={drawerType === 'button' ? '请选择所属菜单' : '请选择上级菜单'} filterable disabled={saving} onChange={(value) => patchDraft({ parent_id: value ? String(value) : null })} /></MenuField>
        {drawerType !== 'button' && <><MenuField label="菜单路由" help="可留空；填写时必须以 / 开头。"><Input value={draft.route} disabled={saving} placeholder="例如 /orders" onChange={(value) => patchDraft({ route: value })} /></MenuField><MenuField label="组件"><Input value={draft.component} disabled={saving} placeholder="例如 /pages/orders" onChange={(value) => patchDraft({ component: value })} /></MenuField><MenuField label="图标"><Input value={draft.icon} disabled={saving} placeholder="可选" onChange={(value) => patchDraft({ icon: value })} /></MenuField></>}
        <MenuField label="排序"><Input type="number" value={String(draft.sort_order)} disabled={saving} placeholder="0" onChange={(value) => patchDraft({ sort_order: Number(value) })} /></MenuField>
        <MenuField label="状态"><div className="resource-menus-switch-field"><span>启用菜单或按钮</span><Switch value={draft.enabled} disabled={saving} aria-label="启用菜单或按钮" onChange={(value) => patchDraft({ enabled: Boolean(value) })} /></div></MenuField>
      </Form>}
    </Drawer>
    <Dialog visible={Boolean(deleteTarget)} header="确认删除" confirmBtn={{ content: '确认删除', theme: 'danger', loading: deleting }} cancelBtn="取消" onConfirm={() => void confirmDelete()} onCancel={() => { if (!deleting) setDeleteTarget(null); }} onClose={() => { if (!deleting) setDeleteTarget(null); }}>
      <div className="resource-menus-delete-dialog">确认删除「{deleteTarget?.name || deleteTarget?.code}」？{deleteError && <Alert theme="error" message={deleteError} />}</div>
    </Dialog>
  </div>;
};

export default Menus;
