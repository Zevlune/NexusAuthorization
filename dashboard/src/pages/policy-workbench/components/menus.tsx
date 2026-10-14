import { type CSSProperties, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Alert, Button, Card, Dialog, Drawer, Dropdown, EnhancedTable, Form, Input, InputNumber, MessagePlugin, Pagination, Radio, Select, Space, Switch, Typography } from 'tdesign-react';
import type { TableProps } from 'tdesign-react/es/table/interface';
import { AddIcon, ChevronDownIcon, ChevronRightIcon, MoreIcon, RefreshIcon, SearchIcon } from 'tdesign-icons-react';
import { deleteMenu, saveMenu, type Menu } from '../../../api/rbac-v1';
import { Empty, fail, Guard, Page, type State } from '../shared';
import MenusParentPicker, { type ParentPickerOption } from './menus-parent-picker';
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
type TypeFilter = 'all' | Menu['menu_type'];

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

const typeLabel: Record<Menu['menu_type'], string> = { menu: '菜单', button: '按钮' };
const typeFilterOptions: { label: string; value: TypeFilter }[] = [
  { label: '全部类型', value: 'all' },
  { label: '菜单', value: 'menu' },
  { label: '按钮', value: 'button' },
];

/** Translate backend tree-rule rejections into user-facing copy. */
const friendlySaveError = (error: unknown): string => {
  const raw = error instanceof Error ? error.message : '';
  if (/button cannot have child/i.test(raw)) return '按钮不能作为上级节点，请选择菜单。';
  if (/cannot become a button/i.test(raw)) return '存在子项的菜单不能改为按钮，请先处理子项。';
  if (/parent outside application/i.test(raw)) return '上级节点不属于当前应用，请重新选择。';
  if (/cyclic/i.test(raw)) return '上级菜单不能选择当前节点或其后代。';
  return raw || '保存失败，请稍后重试';
};

const MenuField = ({ name, label, required, children }: { name?: keyof MenuDraft; label: string; required?: boolean; children: ReactNode }) => (
  <Form.FormItem name={name} label={label} requiredMark={required} rules={required ? [{ required: true, message: `请填写${label}` }] : undefined}>{children}</Form.FormItem>
);

const sortNodes = (nodes: MenuNode[]): MenuNode[] => [...nodes].sort((left, right) => left.sort_order - right.sort_order || left.name.localeCompare(right.name));

/** Build an arbitrary-depth tree from API data. Invalid or cyclic parent links become roots. */
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
  const branchIds = useMemo(() => allNodes.filter((node) => node.children.length).map((node) => node.id), [allNodes]);
  const [form] = Form.useForm();
  const [filterForm] = Form.useForm();
  const [draftSearch, setDraftSearch] = useState('');
  const [draftTypeFilter, setDraftTypeFilter] = useState<TypeFilter>('all');
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [search, setSearch] = useState('');
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all');
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [draft, setDraft] = useState<MenuDraft | null>(null);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<MenuNode | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  useEffect(() => {
    setExpanded((current) => {
      const next = new Set(current);
      tree.filter((node) => node.children.length).forEach((node) => next.add(node.id));
      for (const id of next) if (!branchIds.includes(id)) next.delete(id);
      return next;
    });
  }, [branchIds, tree]);

  const keyword = search.trim().toLowerCase();
  const filtering = Boolean(keyword) || typeFilter !== 'all';
  const visibleTree = useMemo(() => {
    if (!filtering) return tree as MenuTableRow[];
    const matches = (item: MenuNode) => (typeFilter === 'all' || item.menu_type === typeFilter)
      && (!keyword || `${item.name} ${item.code}`.toLowerCase().includes(keyword));
    const filter = (nodes: MenuNode[]): MenuTableRow[] => nodes.flatMap((node) => {
      const matching = matches(node);
      const children = matching && typeFilter === 'all' ? node.children : filter(node.children);
      return matching || children.length ? [{ ...node, children: children as MenuNode[] } as MenuTableRow] : [];
    });
    return filter(tree);
  }, [filtering, keyword, tree, typeFilter]);
  const visibleCount = useMemo(() => flattenNodes(visibleTree as unknown as MenuNode[]).length, [visibleTree]);
  const expandedTreeKeys = filtering
    ? allNodes.filter((node) => node.children.length).map((node) => node.id)
    : Array.from(expanded);

  // Page complete root subtrees so pagination never detaches a child from its parent.
  const pageCount = Math.max(1, Math.ceil(visibleTree.length / pageSize));
  const activePage = Math.min(currentPage, pageCount);
  const pagedTree = visibleTree.slice((activePage - 1) * pageSize, activePage * pageSize);
  useEffect(() => setCurrentPage(1), [search, typeFilter, state.appKey]);
  useEffect(() => { if (currentPage > pageCount) setCurrentPage(pageCount); }, [currentPage, pageCount]);

  const nameColumnWidth = useMemo(() => {
    const expandedIds = new Set(expandedTreeKeys);
    const depthOf = (nodes: MenuNode[], depth: number): number => nodes.reduce((maximum, node) => Math.max(maximum, expandedIds.has(node.id) && node.children.length ? depthOf(node.children, depth + 1) : depth), depth);
    return Math.max(250, 160 + depthOf(pagedTree, 0) * 20);
  }, [expandedTreeKeys, pagedTree]);

  const menuColumns: TableProps<MenuTableRow>['columns'] = [
    { colKey: 'name', title: '名称', width: nameColumnWidth, cell: ({ row }) => <Typography.Text>{row.name || '—'}</Typography.Text> },
    { colKey: 'menu_type', title: '类型', width: 100, cell: ({ row }) => typeLabel[row.menu_type] },
    { colKey: 'code', title: '权限标识', width: 230 },
    { colKey: 'route', title: '路由', width: 180, cell: ({ row }) => row.menu_type === 'button' ? '—' : row.route || '—' },
    { colKey: 'sort_order', title: '排序', width: 70 },
    {
      colKey: 'operation', title: '操作', minWidth: 250, fixed: 'right',
      cell: ({ row }) => <Space className="resource-menus-row-actions" size={12}>
        <Button variant="text" title={`编辑${row.name}`} onClick={() => openEdit(row)}>编辑</Button>
        {row.menu_type === 'button'
          ? <Button variant="text" title={`删除${row.name}`} onClick={() => requestDelete(row)}>删除</Button>
          : <>
            {row.menu_type === 'menu' && <Button variant="text" onClick={() => openCreate('button', row.id)}>添加按钮</Button>}
            <Dropdown trigger="click" placement="bottom-right" options={[
              { content: '添加子菜单', value: 'child', onClick: () => openCreate('menu', row.id) },
              { content: '删除', value: 'delete', theme: 'error', onClick: () => requestDelete(row) },
            ]}>
              <Button variant="text" icon={<MoreIcon />} aria-label={`更多操作：${row.name}`} />
            </Dropdown>
          </>}
      </Space>,
    },
  ];

  const descendantIds = useCallback((id: string): Set<string> => {
    const found = new Set<string>();
    const visit = (nodes: MenuNode[]) => nodes.forEach((node) => { found.add(node.id); visit(node.children); });
    visit(allNodes.find((item) => item.id === id)?.children || []);
    return found;
  }, [allNodes]);

  const pickerOptions = useMemo<ParentPickerOption[]>(() => {
    if (!draft) return [];
    const isButton = draft.menu_type === 'button';
    const blocked = draft.id ? descendantIds(draft.id) : new Set<string>();
    const options: ParentPickerOption[] = [];
    if (!isButton) options.push({ id: '', name: '根节点', code: '', type: 'root', path: '根节点', depth: 0 });
    const visit = (nodes: MenuNode[], depth: number, prefix: string[]) => {
      nodes.forEach((node) => {
        const label = node.name || node.code || node.id;
        const path = [...prefix, label];
        let disabled = false;
        let disabledReason = '';
        if (draft.id && node.id === draft.id) {
          disabled = true;
          disabledReason = '当前编辑节点';
        } else if (node.menu_type === 'button') {
          disabled = true;
          disabledReason = isButton ? '按钮 · 不可作为所属' : '按钮 · 不可作为上级';
        } else if (draft.id && blocked.has(node.id)) {
          disabled = true;
          disabledReason = '当前节点的子级';
        }
        options.push({ id: node.id, name: label, code: node.code, type: node.menu_type, path: path.join(' / '), depth, disabled, disabledReason: disabledReason || undefined });
        visit(node.children, depth + 1, path);
      });
    };
    visit(tree, 0, []);
    return options;
  }, [descendantIds, draft, tree]);

  const pickerNote = draft?.menu_type === 'button'
    ? '按钮是叶节点，不能再添加子级；按钮只能挂在菜单下。'
    : '菜单可挂在任意层级的菜单下；按钮不能作为上级。编辑时自身和后代不可选择。';

  const patchDraft = (update: Partial<MenuDraft>) => {
    form.setFieldsValue(update);
    setDraft((current) => current ? { ...current, ...update } : current);
  };

  const openCreate = (kind: 'menu' | 'button', parentId: string | null = null) => {
    setFormError('');
    const next = makeDraft(undefined, kind, parentId);
    form.reset();
    form.setFieldsValue(next);
    setDraft(next);
  };

  const openEdit = (menu: MenuNode) => {
    setFormError('');
    const next = makeDraft(menu);
    form.reset();
    form.setFieldsValue(next);
    setDraft(next);
  };

  const closeDrawer = () => {
    if (!saving) {
      setDraft(null);
      setFormError('');
    }
  };

  const changeType = (menuType: Menu['menu_type']) => {
    if (!draft) return;
    const parent = draft.parent_id ? allNodes.find((item) => item.id === draft.parent_id) : undefined;
    const parentId = menuType === 'button' && parent?.menu_type !== 'menu' ? null : draft.parent_id;
    patchDraft({
      menu_type: menuType,
      parent_id: parentId,
    });
  };

  const save = async () => {
    if (!draft || saving) return;
    if (await form.validate() !== true) return;
    const code = draft.code.trim();
    const name = draft.name.trim();
    const parsedSort = Number(draft.sort_order);
    const parent = draft.parent_id ? allNodes.find((item) => item.id === draft.parent_id) : undefined;
    let error = '';
    if (!name) error = '请填写名称';
    else if (!code) error = '请填写权限标识';
    else if (!Number.isInteger(parsedSort)) error = '排序必须是整数';
    else if (draft.menu_type === 'button' && draft.id && (allNodes.find((item) => item.id === draft.id)?.children.length ?? 0) > 0) error = '存在子项的菜单不能改为按钮，请先处理子项';
    else if (draft.menu_type === 'button' && (!parent || parent.menu_type !== 'menu')) error = '按钮必须选择所属菜单';
    else if (draft.menu_type !== 'button' && parent?.menu_type === 'button') error = '菜单不能挂在按钮下';
    else if (draft.menu_type !== 'button' && draft.route.trim() && !draft.route.trim().startsWith('/')) error = '菜单路由必须以 / 开头';
    else if (draft.id && draft.parent_id === draft.id) error = '上级菜单不能选择当前节点';
    else if (draft.id && draft.parent_id && descendantIds(draft.id).has(draft.parent_id)) error = '上级菜单不能选择当前节点或其后代';
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
      const message = friendlySaveError(error);
      setFormError(message);
      MessagePlugin.error(message);
    } finally {
      setSaving(false);
    }
  };

  const requestDelete = (node: MenuNode) => {
    const target = allNodes.find((item) => item.id === node.id) || node;
    setDeleteError(target.children.length ? '该节点仍有子项，请先处理子项后再删除。' : '');
    setDeleteTarget(target);
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

  const submitFilters = () => {
    setSearch(draftSearch);
    setTypeFilter(draftTypeFilter);
    setCurrentPage(1);
  };
  const resetFilters = () => {
    filterForm.setFieldsValue({ keyword: '', type: 'all' });
    setDraftSearch('');
    setDraftTypeFilter('all');
    setSearch('');
    setTypeFilter('all');
    setCurrentPage(1);
  };

  const drawerType = draft?.menu_type || 'menu';
  const drawerTitle = draft?.id ? `编辑${typeLabel[drawerType]}` : `新增${drawerType === 'button' ? '按钮' : '菜单'}`;
  const editingHasChildren = Boolean(draft?.id && (allNodes.find((item) => item.id === draft.id)?.children.length ?? 0));
  return <Space direction="vertical" size={0} className="resource-menus">
    <Guard state={state}>
      <Page title="菜单与按钮" actions={<Button theme="primary" icon={<AddIcon />} disabled={state.loading || Boolean(state.loadError)} onClick={() => openCreate('menu')}>添加</Button>}>
        <Card className="page-filter-bar" bodyStyle={{ padding: 0 }}>
          <Form form={filterForm} initialData={{ keyword: '', type: 'all' }} className="workbench-filter-form" layout="inline" colon={false} labelWidth={100} onSubmit={submitFilters}>
            <Form.FormItem name="keyword" label="关键词">
              <Input className="resource-menus-search workbench-keyword" placeholder="搜索菜单名称或权限标识" clearable prefixIcon={<SearchIcon />} onChange={setDraftSearch} />
            </Form.FormItem>
            <Form.FormItem name="type" label="类型">
              <Select className="resource-menus-type-filter workbench-select" options={typeFilterOptions} onChange={(value) => setDraftTypeFilter(value as TypeFilter)} />
            </Form.FormItem>
            <Form.FormItem><Space size={16}>
              <Button theme="primary" type="submit" icon={<SearchIcon />}>查询</Button>
              <Button theme="primary" icon={<RefreshIcon />} onClick={resetFilters}>重置</Button>
            </Space></Form.FormItem>
          </Form>
        </Card>
        <Card className="resource-menus-panel" bodyStyle={{ padding: 0 }} aria-label="菜单与按钮列表">
          <EnhancedTable<MenuTableRow>
            className="resource-menus-table"
            rowKey="id"
            columns={menuColumns}
            data={pagedTree}
            tree={{ indent: 20 }}
            style={{ '--menu-table-width': `${830 + nameColumnWidth}px` } as CSSProperties}
            treeExpandAndFoldIcon={({ type }) => type === 'fold' ? <ChevronDownIcon /> : <ChevronRightIcon />}
            expandedTreeNodes={expandedTreeKeys}
            onExpandedTreeNodesChange={(keys) => { if (!filtering) setExpanded(new Set(keys.map(String))); }}
            loading={state.loading}
            empty={<Empty text={state.menus.length ? '没有匹配的菜单或按钮' : '尚无菜单或按钮'} />}
            hover
          />
          <Space className="resource-menus-footer" align="center" size={12}>
            <Typography.Text>共 {visibleCount} 条</Typography.Text>
            <Pagination current={activePage} pageSize={pageSize} total={visibleTree.length} totalContent={false} pageSizeOptions={[25, 50, 100]} showJumper={false}
              onChange={(info) => { setCurrentPage(info.pageSize === pageSize ? info.current : 1); setPageSize(info.pageSize); }} />
          </Space>
        </Card>
      </Page>
    </Guard>
    <Drawer className={`resource-menus-drawer resource-menus-drawer--${drawerType}`} visible={Boolean(draft)} header={drawerTitle} size="560px" closeOnOverlayClick={!saving} onClose={closeDrawer} footer={<Space className="resource-menus-drawer-footer" size={12}><Button variant="outline" disabled={saving} onClick={closeDrawer}>取消</Button><Button theme="primary" loading={saving} onClick={() => void save()}>{drawerType === 'button' ? '保存按钮' : '保存菜单'}</Button></Space>}>
      {draft && <Form form={form} initialData={draft} className="resource-menus-form" labelAlign="top" requiredMarkPosition="right" colon={false} disabled={saving}>
        {formError && <Alert className="resource-menus-form-error" theme="error" message={formError} />}
        <Typography.Paragraph className="resource-menus-form-help">{drawerType === 'button' ? '可选择任意层级的菜单作为所属节点。按钮是叶节点，不能再添加子级。' : '菜单可挂在任意层级的菜单下，按钮不能作为上级。'}</Typography.Paragraph>
        <MenuField name="menu_type" label="资源类型">
          <Radio.Group className="resource-menus-type-radio" onChange={(value) => changeType(value as Menu['menu_type'])}>
            <Radio value="menu">菜单</Radio>
            <Radio value="button" disabled={editingHasChildren} title={editingHasChildren ? '存在子项的节点不能改为按钮' : undefined}>按钮</Radio>
          </Radio.Group>
        </MenuField>
        <MenuField name="name" label={drawerType === 'button' ? '按钮名称' : '菜单名称'} required><Input placeholder={drawerType === 'button' ? '例如：创建订单' : '例如：订单管理'} onChange={(value) => patchDraft({ name: value })} /></MenuField>
        <MenuField name="code" label="权限标识" required><Input disabled={saving || Boolean(draft.id)} placeholder={drawerType === 'button' ? '例如：orders:create' : '例如：orders'} onChange={(value) => patchDraft({ code: value })} /></MenuField>
        <MenuField name="parent_id" label={drawerType === 'button' ? '所属菜单' : '上级菜单'} required={drawerType === 'button'}>
          <MenusParentPicker value={draft.parent_id} options={pickerOptions} note={pickerNote} disabled={saving} placeholder={drawerType === 'button' ? '请选择所属菜单' : '根节点'} onChange={(value) => patchDraft({ parent_id: value })} />
        </MenuField>
        {drawerType !== 'button' && <>
          <MenuField name="route" label="路由（选填）"><Input placeholder="例如：/orders" onChange={(value) => patchDraft({ route: value })} /></MenuField>
          <MenuField name="component" label="组件（选填）"><Input placeholder="例如：orders/index" onChange={(value) => patchDraft({ component: value })} /></MenuField>
        </>}
        <MenuField name="sort_order" label="排序"><InputNumber theme="normal" decimalPlaces={0} onChange={(value) => patchDraft({ sort_order: value as number })} /></MenuField>
        <Space className="resource-menus-switch-field" align="center" size={12}>
          <Typography.Text>{drawerType === 'button' ? '启用按钮' : '启用菜单'}</Typography.Text>
          <Switch value={draft.enabled} disabled={saving} aria-label={drawerType === 'button' ? '启用按钮' : '启用菜单'} onChange={(value) => patchDraft({ enabled: Boolean(value) })} />
        </Space>
      </Form>}
    </Drawer>
    <Dialog visible={Boolean(deleteTarget)} header={`删除${typeLabel[deleteTarget?.menu_type || 'menu']}`} theme="warning" confirmBtn={{ content: '确认删除', theme: 'danger', loading: deleting, disabled: Boolean(deleteTarget?.children.length) }} cancelBtn={{ content: '取消', disabled: deleting }} closeOnOverlayClick={!deleting} onConfirm={() => void confirmDelete()} onCancel={() => { if (!deleting) setDeleteTarget(null); }} onClose={() => { if (!deleting) setDeleteTarget(null); }}>
      <Space direction="vertical" size={16} className="resource-menus-delete-dialog">
        <Typography.Paragraph>确认删除「{deleteTarget?.name || deleteTarget?.code}」？删除后将无法恢复。</Typography.Paragraph>
        {deleteError && <Alert theme="error" message={deleteError} />}
      </Space>
    </Dialog>
  </Space>;
};

export default Menus;
