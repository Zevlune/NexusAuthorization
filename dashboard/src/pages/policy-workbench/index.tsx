import { useCallback, useEffect, useState } from 'react';
import { Button, Card, Col, Descriptions, DialogPlugin, Form, Input, Row, Select, Space, Statistic, Steps, Table, Tag, Pagination, Typography, type TableProps } from 'tdesign-react';
import { AddIcon, DeleteIcon, EditIcon, RefreshIcon, SearchIcon } from 'tdesign-icons-react';
import { getStoredApplication, listApplicationServiceResources, type ApplicationServiceResource } from '../../api/application';
import { deleteRole, listAllEndpoints, listMenus, listRoles, listSubjectRoles, type Endpoint, type Menu, type Role, type SubjectRole } from '../../api/rbac-v1';
import { State, Page, Guard, Empty, fail } from './shared';
import Services from './components/services';
import APIs from './components/apis';
import Menus from './components/menus';
import RoleUsers from './components/role-users';
import RoleEditor from './components/role-editor';
import Simulate from './components/simulate';
import { useLocation, useNavigate } from 'react-router-dom';
import { getRequestErrorMessage } from '../nexus-authorization/shared';
import './style.less';

const useRBAC = (): State => {
  const [appKey, setAppKey] = useState(getStoredApplication);
  const [services, setServices] = useState<ApplicationServiceResource[]>([]);
  const [endpoints, setEndpoints] = useState<Endpoint[]>([]);
  const [roles, setRoles] = useState<Role[]>([]);
  const [menus, setMenus] = useState<Menu[]>([]);
  const [bindings, setBindings] = useState<SubjectRole[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  useEffect(() => {
    const sync = () => setAppKey(getStoredApplication());
    window.addEventListener('nexus-authorization-application-change', sync);
    return () => window.removeEventListener('nexus-authorization-application-change', sync);
  }, []);
  const reload = useCallback(async () => {
    if (!appKey) { setLoading(false); return; }
    setLoading(true);
    setLoadError('');
    try {
      const [serviceResult, endpointResult, roleResult, menuResult, bindingResult] = await Promise.all([
        listApplicationServiceResources(appKey), listAllEndpoints(appKey), listRoles(appKey), listMenus(appKey), listSubjectRoles(appKey),
      ]);
      setServices(serviceResult);
      setEndpoints(endpointResult);
      setRoles(roleResult.items || []);
      setMenus(menuResult.items || []);
      setBindings(bindingResult.items || []);
    } catch (error) { setLoadError(getRequestErrorMessage(error, '加载应用权限配置失败')); }
    finally { setLoading(false); }
  }, [appKey]);
  useEffect(() => { void reload(); }, [reload]);
  return { appKey, services, endpoints, roles, menus, bindings, loading, loadError, reload };
};

const Overview = ({ state }: { state: State }) => {
  const [compact, setCompact] = useState(() => window.matchMedia('(max-width: 760px)').matches);
  useEffect(() => { const media = window.matchMedia('(max-width: 760px)'); const sync = () => setCompact(media.matches); media.addEventListener('change', sync); return () => media.removeEventListener('change', sync); }, []);
  return <Guard state={state}><Page title="应用权限总览">
  <Card className="rbac-card"><Steps className="rbac-overview-steps" layout={compact ? 'vertical' : 'horizontal'} readOnly current={0} options={[
    { title: '关联服务', content: '选择应用可使用的服务资源' },
    { title: '配置角色', content: '跨服务勾选 API 与菜单' },
    { title: '绑定用户', content: '为用户绑定应用角色' },
    { title: '验证权限', content: '保存后立即生效，可模拟请求验证' },
  ]} /></Card>
  <Card className="rbac-card"><Descriptions items={[{ label: '当前应用', content: state.appKey }]} column={1} /><Row gutter={[16, 16]}>
    <Col xs={12} sm={6}><Statistic title="关联服务" value={state.services.length} /></Col>
    <Col xs={12} sm={6}><Statistic title="API 接口" value={state.endpoints.length} /></Col>
    <Col xs={12} sm={6}><Statistic title="角色" value={state.roles.length} /></Col>
    <Col xs={12} sm={6}><Statistic title="菜单与按钮" value={state.menus.length} /></Col>
  </Row></Card>
</Page></Guard>;
};

const Roles = ({ state }: { state: State }) => {
  const navigate = useNavigate();
  const [filterForm] = Form.useForm();
  const [rolePage, setRolePage] = useState(1);
  const [listSearch, setListSearch] = useState('');
  const [draftSearch, setDraftSearch] = useState('');
  const [roleStatus, setRoleStatus] = useState<'all' | 'enabled' | 'disabled'>('all');
  const [draftRoleStatus, setDraftRoleStatus] = useState<'all' | 'enabled' | 'disabled'>('all');
  const [assignmentRole, setAssignmentRole] = useState<Role | null>(null);
  const confirmDelete = (role: Role) => {
    let deleting = false;
    const confirmBtn = { content: '确认删除', theme: 'danger' as const };
    const dialog = DialogPlugin.confirm({ header: '删除角色', body: `确认删除“${role.name}”及其授权和用户绑定吗？`, confirmBtn, onConfirm: async () => {
      // Ignore repeated clicks while the delete is in flight.
      if (deleting) return;
      deleting = true;
      dialog.update({ confirmBtn: { ...confirmBtn, loading: true } });
      try { await deleteRole(state.appKey, role.id); dialog.destroy(); await state.reload(); } catch (error) { deleting = false; dialog.update({ confirmBtn }); fail(error, '删除角色失败'); }
    } });
  };

  const columns: TableProps<Role>['columns'] = [
    { colKey: 'code', title: '角色编码', minWidth: 140, cell: ({ row }) => <code>{row.code}</code> },
    { colKey: 'name', title: '名称', minWidth: 140 },
    { colKey: 'endpoint_ids', title: 'API', width: 90, cell: ({ row }) => row.endpoint_ids?.length || 0 },
    { colKey: 'menu_ids', title: '菜单', width: 90, cell: ({ row }) => row.menu_ids?.length || 0 },
    { colKey: 'enabled', title: '状态', width: 90, cell: ({ row }) => <Tag theme={row.enabled ? 'success' : 'default'} variant="light-outline">{row.enabled ? '启用' : '停用'}</Tag> },
    { colKey: 'actions', title: '操作', minWidth: 300, fixed: 'right', cell: ({ row }) => <Space size={0}><Button variant="text" icon={<EditIcon />} disabled={state.loading || Boolean(state.loadError)} onClick={() => navigate(`/roles/${encodeURIComponent(row.id)}/edit`)}>编辑授权</Button><Button variant="text" theme="primary" disabled={state.loading || Boolean(state.loadError)} onClick={() => setAssignmentRole(row)}>分配用户</Button><Button variant="text" theme="danger" icon={<DeleteIcon />} onClick={() => confirmDelete(row)}>删除</Button></Space> },
  ];
  const filteredRoles = state.roles.filter(role => {
    const matchesSearch = `${role.name} ${role.code}`.toLowerCase().includes(listSearch.trim().toLowerCase());
    const matchesStatus = roleStatus === 'all' || (roleStatus === 'enabled' ? role.enabled : !role.enabled);
    return matchesSearch && matchesStatus;
  });
  const displayRolePage = Math.min(rolePage, Math.max(1, Math.ceil(filteredRoles.length / 25)));
  const visibleRoles = filteredRoles.slice((displayRolePage - 1) * 25, displayRolePage * 25);
  const submitListFilter = () => {
    setRolePage(1);
    setListSearch(draftSearch);
    setRoleStatus(draftRoleStatus);
  };
  const resetListFilter = () => {
    filterForm.setFieldsValue({ keyword: '', status: 'all' });
    setRolePage(1);
    setDraftSearch('');
    setDraftRoleStatus('all');
    setListSearch('');
    setRoleStatus('all');
  };
  return <Space direction="vertical" size={0} className="role-management"><Guard state={state}><Page title="角色管理" actions={<Button theme="primary" icon={<AddIcon />} disabled={state.loading || Boolean(state.loadError)} onClick={() => navigate('/roles/new')}>新增角色</Button>}>
    <Card className="page-filter-bar" bodyStyle={{ padding: 0 }}>
      <Form form={filterForm} initialData={{ keyword: '', status: 'all' }} className="workbench-filter-form" layout="inline" colon={false} labelWidth={100} onSubmit={submitListFilter}>
        <Form.FormItem name="keyword" label="关键词">
          <Input className="workbench-keyword" clearable placeholder="搜索角色名称或编码" prefixIcon={<SearchIcon />} onChange={setDraftSearch} />
        </Form.FormItem>
        <Form.FormItem name="status" label="状态">
          <Select className="workbench-select" options={[{ label: '全部状态', value: 'all' }, { label: '启用', value: 'enabled' }, { label: '停用', value: 'disabled' }]} onChange={value => setDraftRoleStatus(value as 'all' | 'enabled' | 'disabled')} />
        </Form.FormItem>
        <Form.FormItem><Space size={16}>
          <Button type="submit" theme="primary" icon={<SearchIcon />}>查询</Button>
          <Button type="button" theme="primary" icon={<RefreshIcon />} onClick={resetListFilter}>重置</Button>
        </Space></Form.FormItem>
      </Form>
    </Card>
    <Card className="rbac-card rbac-table-card role-management-panel" bodyStyle={{ padding: 0 }}>
      <Table<Role> rowKey="id" data={visibleRoles} columns={columns} loading={state.loading} hover tableLayout="auto" empty={<Empty text={state.roles.length ? '没有匹配的角色' : '尚无角色'} />} />
      <Space className="role-management-footer" align="center" size={12}>
        <Typography.Text>共 {filteredRoles.length} 个角色</Typography.Text>
        <Pagination current={displayRolePage} total={filteredRoles.length} totalContent={false} pageSize={25} showPageSize={false} onCurrentChange={setRolePage} />
      </Space>
    </Card>
    {assignmentRole ? <RoleUsers key={assignmentRole.id} state={state} role={assignmentRole} onClose={() => setAssignmentRole(null)} /> : null}
  </Page></Guard></Space>;
};

export default function WorkbenchPage() { const state = useRBAC(); const location = useLocation(); if (location.pathname.startsWith('/roles/')) return <RoleEditor key={`${state.appKey}:${location.pathname}`} state={state} />; switch (location.pathname) { case '/service-resources': return <Services state={state} />; case '/api-resources': return <APIs state={state} />; case '/roles': return <Roles key={state.appKey} state={state} />; case '/menus': return <Menus state={state} />; case '/simulate': return <Simulate key={state.appKey} state={state} />; default: return <Overview state={state} />; } }
