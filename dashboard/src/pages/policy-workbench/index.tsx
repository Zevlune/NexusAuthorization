import { useCallback, useEffect, useState } from 'react';
import { Button, Card, Col, Descriptions, DialogPlugin, Form, Input, Row, Select, Space, Statistic, Steps, Table, Tag, Pagination, Typography, type TableProps } from 'tdesign-react';
import { AddIcon, DeleteIcon, EditIcon, RefreshIcon, SearchIcon } from 'tdesign-icons-react';
import { getStoredApplication, listApplicationServiceResources, type ApplicationServiceResource } from '../../api/application';
import { deleteRole, listAllEndpoints, listMenus, listRBACReleases, listRoles, listSubjectRoles, publishRBAC, simulateRBAC, type Endpoint, type Menu, type Release, type Role, type SubjectRole } from '../../api/rbac-v1';
import { State, Page, Guard, Empty, fail } from './shared';
import Services from './components/services';
import APIs from './components/apis';
import Menus from './components/menus';
import RoleUsers from './components/role-users';
import RoleEditor from './components/role-editor';
import { useLocation, useNavigate } from 'react-router-dom';
import { getRequestErrorMessage } from '../nexus-authorization/shared';
import '../nexus-authorization/style.less';
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
    { title: '发布验证', content: '生成应用权限快照' },
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
    const dialog = DialogPlugin.confirm({ header: '删除角色', body: `确认删除“${role.name}”及其授权和用户绑定吗？`, confirmBtn: { content: '确认删除', theme: 'danger' }, onConfirm: async () => {
      try { await deleteRole(state.appKey, role.id); dialog.destroy(); await state.reload(); } catch (error) { fail(error, '删除角色失败'); }
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

const Releases = ({ state }: { state: State }) => {
  const [items, setItems] = useState<Release[]>([]);
  const [description, setDescription] = useState('');
  const [loading, setLoading] = useState(true);
  const [publishing, setPublishing] = useState(false);
  const load = useCallback(async () => { setLoading(true); try { setItems((await listRBACReleases(state.appKey)).items || []); } catch (error) { fail(error, '加载发布版本失败'); } finally { setLoading(false); } }, [state.appKey]);
  useEffect(() => { void load(); }, [load]);
  const publish = async () => { if (publishing) return; setPublishing(true); try { await publishRBAC(state.appKey, description || '发布应用权限配置'); await load(); } catch (error) { fail(error, '发布失败'); } finally { setPublishing(false); } };
  const columns: TableProps<Release>['columns'] = [
    { colKey: 'version', title: '版本', width: 100, cell: ({ row }) => `v${row.version}` },
    { colKey: 'status', title: '状态', width: 120, cell: ({ row }) => <Tag theme={row.status === 'published' ? 'success' : 'default'} variant="light-outline">{row.status === 'published' ? '当前发布' : '已替代'}</Tag> },
    { colKey: 'description', title: '说明', minWidth: 200 },
    { colKey: 'published_at', title: '时间', minWidth: 200 },
  ];
  return <Guard state={state}><Page title="发布与版本" actions={<Button theme="primary" loading={publishing} onClick={() => void publish()}>发布当前配置</Button>}>
    <Card className="rbac-card"><Form labelAlign="top" colon={false}><Form.FormItem label="版本说明"><><Input value={description} onChange={setDescription} /></></Form.FormItem></Form></Card>
    <Card className="rbac-card rbac-table-card"><Table<Release> rowKey="id" data={items} columns={columns} loading={loading} hover tableLayout="auto" empty={<Empty text="尚无发布版本" />} /></Card>
  </Page></Guard>;
};

const Simulate = ({ state }: { state: State }) => {
  const [form, setForm] = useState({ service_resource: '', subject: '', domain: 'global', method: 'GET', path: '' });
  const [result, setResult] = useState<unknown>(null);
  const [running, setRunning] = useState(false);
  useEffect(() => { setForm(current => ({ ...current, service_resource: current.service_resource || state.services.find(item => item.isActive)?.key || '' })); }, [state.services]);
  const run = async () => { if (running) return; setRunning(true); try { setResult(await simulateRBAC(state.appKey, form)); } catch (error) { fail(error, '权限测试失败'); } finally { setRunning(false); } };
  return <Guard state={state}><Page title="权限模拟" actions={<Button theme="primary" loading={running} onClick={() => void run()}>运行测试</Button>}>
    <Card className="rbac-card"><Form labelAlign="top" colon={false} onSubmit={() => void run()}><Row gutter={[16, 0]}>
      <Col xs={12} md={6}><Form.FormItem label="目标服务"><><Select value={form.service_resource} options={state.services.filter(item => item.isActive).map(item => ({ label: `${item.displayName || item.key} · ${item.key}`, value: item.key }))} onChange={value => setForm({ ...form, service_resource: String(value) })} /></></Form.FormItem></Col>
      <Col xs={12} md={6}><Form.FormItem label="Subject"><><Input value={form.subject} onChange={value => setForm({ ...form, subject: value })} /></></Form.FormItem></Col>
      <Col xs={12} md={6}><Form.FormItem label="Domain"><><Input value={form.domain} onChange={value => setForm({ ...form, domain: value })} /></></Form.FormItem></Col>
      <Col xs={12} md={6}><Form.FormItem label="HTTP 方法"><><Select value={form.method} options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(value => ({ label: value, value }))} onChange={value => setForm({ ...form, method: String(value) })} /></></Form.FormItem></Col>
      <Col span={12}><Form.FormItem label="Path"><><Input value={form.path} onChange={value => setForm({ ...form, path: value })} /></></Form.FormItem></Col>
    </Row></Form></Card>
    <Card className="rbac-card" title="测试结果">{result && typeof result === 'object' ? <Descriptions column={1} bordered items={Object.entries(result).map(([label, value]) => ({ label, content: typeof value === 'boolean' ? <Tag theme={value ? 'success' : 'danger'}>{String(value)}</Tag> : Array.isArray(value) ? value.join('、') || '-' : String(value ?? '-') }))} /> : <Empty text="输入请求后运行测试" />}</Card>
  </Page></Guard>;
};

export default function WorkbenchPage() { const state = useRBAC(); const location = useLocation(); if (location.pathname.startsWith('/roles/')) return <RoleEditor key={`${state.appKey}:${location.pathname}`} state={state} />; switch (location.pathname) { case '/service-resources': return <Services state={state} />; case '/api-resources': return <APIs state={state} />; case '/roles': return <Roles key={state.appKey} state={state} />; case '/menus': return <Menus state={state} />; case '/releases': return <Releases state={state} />; case '/simulate': return <Simulate state={state} />; default: return <Overview state={state} />; } }
