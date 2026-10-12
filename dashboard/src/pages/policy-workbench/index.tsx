import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Card, Checkbox, Col, Descriptions, DialogPlugin, Drawer, Form, Input, MessagePlugin, Row, Select, Space, Statistic, Steps, Switch, Table, Tag, Textarea, type TableProps } from 'tdesign-react';
import { AddIcon, DeleteIcon, EditIcon, RefreshIcon, SearchIcon } from 'tdesign-icons-react';
import { getStoredApplication, listApplicationServiceResources, type ApplicationServiceResource } from '../../api/application';
import { deleteRole, listAllEndpoints, listMenus, listRBACReleases, listRoles, listSubjectRoles, publishRBAC, replaceSubjectRoles, saveRole, simulateRBAC, type Endpoint, type Menu, type Release, type Role, type SubjectRole } from '../../api/rbac-v1';
import { State, Page, Guard, Empty, fail } from './shared';
import Services from './components/services';
import APIs from './components/apis';
import Menus from './components/menus';
import { useLocation } from 'react-router-dom';
import { getRequestErrorMessage } from '../permission-center/shared';
import '../permission-center/style.less';
import './style.less';

const methodTheme = (method: string): 'primary' | 'success' | 'warning' | 'danger' | 'default' => ({ GET: 'primary', POST: 'success', PUT: 'warning', PATCH: 'warning', DELETE: 'danger' }[method] || 'default') as 'primary' | 'success' | 'warning' | 'danger' | 'default';

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
    window.addEventListener('permission-center-application-change', sync);
    return () => window.removeEventListener('permission-center-application-change', sync);
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
  const [edit, setEdit] = useState<Partial<Role> | null>(null);
  const [listSearch, setListSearch] = useState('');
  const [draftSearch, setDraftSearch] = useState('');
  const [roleStatus, setRoleStatus] = useState<'all' | 'enabled' | 'disabled'>('all');
  const [draftRoleStatus, setDraftRoleStatus] = useState<'all' | 'enabled' | 'disabled'>('all');
  const [filter, setFilter] = useState('');
  const [saving, setSaving] = useState(false);
  const endpoints = useMemo(() => state.endpoints.filter(item => `${item.service_resource} ${item.method} ${item.path_template} ${item.module} ${item.summary}`.toLowerCase().includes(filter.toLowerCase())), [state.endpoints, filter]);
  const menus = useMemo(() => state.menus.filter(item => `${item.code} ${item.name}`.toLowerCase().includes(filter.toLowerCase())), [state.menus, filter]);
  const save = async () => {
    if (saving) return;
    if (!edit?.code?.trim() || !edit.name?.trim()) { MessagePlugin.warning('角色编码和名称为必填'); return; }
    setSaving(true);
    try { await saveRole(state.appKey, { ...edit, code: edit.code.trim(), name: edit.name.trim(), endpoint_ids: edit.endpoint_ids || [], menu_ids: edit.menu_ids || [], enabled: edit.enabled ?? true }); setEdit(null); await state.reload(); }
    catch (error) { fail(error, '保存角色失败'); }
    finally { setSaving(false); }
  };
  const close = () => { if (!saving) setEdit(null); };
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
    { colKey: 'actions', title: '操作', minWidth: 210, fixed: 'right', cell: ({ row }) => <Space size={0}><Button variant="text" icon={<EditIcon />} onClick={() => { setFilter(''); setEdit({ ...row, endpoint_ids: row.endpoint_ids || [], menu_ids: row.menu_ids || [] }); }}>编辑授权</Button><Button variant="text" theme="danger" icon={<DeleteIcon />} onClick={() => confirmDelete(row)}>删除</Button></Space> },
  ];
  const visibleRoles = state.roles.filter(role => {
    const matchesSearch = `${role.name} ${role.code}`.toLowerCase().includes(listSearch.trim().toLowerCase());
    const matchesStatus = roleStatus === 'all' || (roleStatus === 'enabled' ? role.enabled : !role.enabled);
    return matchesSearch && matchesStatus;
  });
  const submitListFilter = () => {
    setListSearch(draftSearch);
    setRoleStatus(draftRoleStatus);
  };
  const resetListFilter = () => {
    setDraftSearch('');
    setDraftRoleStatus('all');
    setListSearch('');
    setRoleStatus('all');
  };
  return <Guard state={state}><Page title="角色管理" actions={<Button theme="primary" icon={<AddIcon />} onClick={() => setEdit({ code: '', name: '', description: '', enabled: true, endpoint_ids: [], menu_ids: [] })}>新增角色</Button>}>
    <div className="page-filter-bar">
      <Form className="workbench-filter-form" layout="inline" colon={false} onSubmit={submitListFilter}>
        <Form.FormItem label="关键词" labelWidth={100}>
          <Input style={{ width: 280 }} value={draftSearch} clearable placeholder="搜索角色名称或编码" prefixIcon={<SearchIcon />} onChange={setDraftSearch} />
        </Form.FormItem>
        <Form.FormItem label="状态" labelWidth={100}>
          <Select value={draftRoleStatus} options={[{ label: '全部状态', value: 'all' }, { label: '启用', value: 'enabled' }, { label: '停用', value: 'disabled' }]} onChange={value => setDraftRoleStatus(value as 'all' | 'enabled' | 'disabled')} />
        </Form.FormItem>
        <Space size={16}>
          <Button type="submit" theme="primary" icon={<SearchIcon />}>查询</Button>
          <Button type="button" theme="primary" variant="outline" icon={<RefreshIcon />} onClick={resetListFilter}>重置</Button>
        </Space>
      </Form>
    </div>
    <Card className="rbac-card rbac-table-card"><Table<Role> rowKey="id" data={visibleRoles} columns={columns} loading={state.loading} hover tableLayout="auto" empty={<Empty text="尚无角色" />} /></Card>
    <Drawer className="rbac-drawer" visible={Boolean(edit)} header={edit?.id ? '编辑角色授权' : '新增角色授权'} size="820px" confirmBtn={{ content: '保存角色', loading: saving, disabled: saving }} cancelBtn={{ content: '取消', disabled: saving }} onConfirm={() => void save()} onClose={close} onCancel={close} closeOnOverlayClick={false} closeOnEscKeydown={!saving}>
      <Form className="rbac-form" labelAlign="top" colon={false} onSubmit={() => void save()}>
        <Form.FormItem label="角色编码" requiredMark><Input disabled={Boolean(edit?.id) || saving} value={edit?.code || ''} onChange={value => setEdit({ ...edit, code: value })} /></Form.FormItem>
        <Form.FormItem label="角色名称" requiredMark><Input disabled={saving} value={edit?.name || ''} onChange={value => setEdit({ ...edit, name: value })} /></Form.FormItem>
        <Form.FormItem label="说明"><Textarea disabled={saving} value={edit?.description || ''} onChange={value => setEdit({ ...edit, description: value })} /></Form.FormItem>
        <Form.FormItem label="筛选权限"><Input value={filter} placeholder="搜索服务、方法、路径、菜单" onChange={setFilter} /></Form.FormItem>
        <Form.FormItem label={`API 授权 · 已选择 ${edit?.endpoint_ids?.length || 0} 个`}>
          <Checkbox.Group className="rbac-framework-permissions" disabled={saving} value={edit?.endpoint_ids || []} onChange={value => setEdit(current => ({ ...current, endpoint_ids: value.map(String) }))}>
            {endpoints.map(item => <Checkbox key={item.id} value={item.id}><Space direction="vertical" size={2}><Space size={4}><Tag variant="light-outline">{item.service_resource}</Tag><Tag theme={methodTheme(item.method)}>{item.method}</Tag><code>{item.path_template}</code></Space><span>{item.module || '未分类'}{item.summary ? ` · ${item.summary}` : ''}</span></Space></Checkbox>)}
          </Checkbox.Group>
          {!endpoints.length ? <Empty text="没有匹配的 API 权限" /> : null}
        </Form.FormItem>
        <Form.FormItem label={`菜单与按钮授权 · 已选择 ${edit?.menu_ids?.length || 0} 个`}>
          <Checkbox.Group className="rbac-framework-permissions" disabled={saving} value={edit?.menu_ids || []} onChange={value => setEdit(current => ({ ...current, menu_ids: value.map(String) }))}>
            {menus.map(item => <Checkbox key={item.id} value={item.id}><Space direction="vertical" size={2}><Space size={4}><Tag variant="light-outline">{item.menu_type === 'button' ? '按钮' : item.menu_type === 'directory' ? '目录' : '菜单'}</Tag><strong>{item.name}</strong></Space><code>{item.code} · {item.route || '操作权限'}</code></Space></Checkbox>)}
          </Checkbox.Group>
          {!menus.length ? <Empty text="没有匹配的菜单或按钮" /> : null}
        </Form.FormItem>
        <Form.FormItem label="启用角色"><Switch value={edit?.enabled ?? true} disabled={saving} onChange={value => setEdit({ ...edit, enabled: Boolean(value) })} /></Form.FormItem>
      </Form>
    </Drawer>
  </Page></Guard>;
};

type SubjectGroup = { key: string; subject: string; domain: string; roles: string[] };
const Users = ({ state }: { state: State }) => {
  const [open, setOpen] = useState(false);
  const [subject, setSubject] = useState('');
  const [domain, setDomain] = useState('global');
  const [roleIDs, setRoleIDs] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [listSearch, setListSearch] = useState('');
  const [draftSearch, setDraftSearch] = useState('');
  const [domainFilter, setDomainFilter] = useState('');
  const [draftDomainFilter, setDraftDomainFilter] = useState('');
  const groups = Object.values(state.bindings.reduce<Record<string, SubjectGroup>>((all, binding) => {
    const key = JSON.stringify([binding.subject, binding.domain]);
    (all[key] ||= { key, subject: binding.subject, domain: binding.domain, roles: [] }).roles.push(binding.role_id); return all;
  }, {}));
  const save = async () => {
    if (saving) return;
    if (!subject.trim() || !domain.trim()) { MessagePlugin.warning('Subject 和 Domain 为必填'); return; }
    setSaving(true);
    try { await replaceSubjectRoles(state.appKey, subject.trim(), domain.trim(), roleIDs); setOpen(false); await state.reload(); }
    catch (error) { fail(error, '绑定角色失败'); }
    finally { setSaving(false); }
  };
  const close = () => { if (!saving) setOpen(false); };
  const columns: TableProps<SubjectGroup>['columns'] = [
    { colKey: 'subject', title: 'Subject', minWidth: 160, cell: ({ row }) => <code>{row.subject}</code> },
    { colKey: 'domain', title: 'Domain', minWidth: 120 },
    { colKey: 'roles', title: '角色', minWidth: 200, cell: ({ row }) => <Space breakLine size={4}>{row.roles.map(id => <Tag key={id} variant="light-outline">{state.roles.find(role => role.id === id)?.name || id}</Tag>)}</Space> },
    { colKey: 'actions', title: '操作', width: 100, fixed: 'right', cell: ({ row }) => <Button variant="text" icon={<EditIcon />} onClick={() => { setSubject(row.subject); setDomain(row.domain); setRoleIDs(row.roles); setOpen(true); }}>编辑</Button> },
  ];
  const visibleGroups = groups.filter(item => {
    const matchesSearch = `${item.subject} ${item.domain}`.toLowerCase().includes(listSearch.trim().toLowerCase());
    const matchesDomain = item.domain.toLowerCase().includes(domainFilter.trim().toLowerCase());
    return matchesSearch && matchesDomain;
  });
  const submitListFilter = () => {
    setListSearch(draftSearch);
    setDomainFilter(draftDomainFilter);
  };
  const resetListFilter = () => {
    setDraftSearch('');
    setDraftDomainFilter('');
    setListSearch('');
    setDomainFilter('');
  };
  return <Guard state={state}><Page title="用户角色" actions={<Button theme="primary" icon={<AddIcon />} onClick={() => { setSubject(''); setDomain('global'); setRoleIDs([]); setOpen(true); }}>绑定角色</Button>}>
    <div className="page-filter-bar">
      <Form className="workbench-filter-form" layout="inline" colon={false} onSubmit={submitListFilter}>
        <Form.FormItem label="关键词" labelWidth={100}>
          <Input style={{ width: 280 }} value={draftSearch} clearable placeholder="搜索 Subject 或 Domain" prefixIcon={<SearchIcon />} onChange={setDraftSearch} />
        </Form.FormItem>
        <Form.FormItem label="Domain" labelWidth={100}>
          <Input style={{ width: 140 }} value={draftDomainFilter} clearable placeholder="筛选 Domain" onChange={setDraftDomainFilter} />
        </Form.FormItem>
        <Space size={16}>
          <Button type="submit" theme="primary" icon={<SearchIcon />}>查询</Button>
          <Button type="button" theme="primary" variant="outline" icon={<RefreshIcon />} onClick={resetListFilter}>重置</Button>
        </Space>
      </Form>
    </div>
    <Card className="rbac-card rbac-table-card"><Table<SubjectGroup> rowKey="key" data={visibleGroups} columns={columns} loading={state.loading} hover tableLayout="auto" empty={<Empty text="尚无用户角色绑定" />} /></Card>
    <Drawer className="rbac-drawer" visible={open} header="绑定用户角色" size="520px" confirmBtn={{ content: '保存', loading: saving, disabled: saving }} cancelBtn={{ content: '取消', disabled: saving }} onConfirm={() => void save()} onClose={close} onCancel={close} closeOnOverlayClick={false} closeOnEscKeydown={!saving}>
      <Form className="rbac-form" labelAlign="top" colon={false} onSubmit={() => void save()}>
        <Form.FormItem label="Subject" requiredMark><Input value={subject} disabled={saving} onChange={setSubject} /></Form.FormItem>
        <Form.FormItem label="Domain" requiredMark><Input value={domain} disabled={saving} onChange={setDomain} /></Form.FormItem>
        <Form.FormItem label="角色"><Select multiple value={roleIDs} disabled={saving} options={state.roles.filter(role => role.enabled).map(role => ({ label: `${role.name} · ${role.code}`, value: role.id }))} onChange={value => setRoleIDs((value as string[]).map(String))} /></Form.FormItem>
      </Form>
    </Drawer>
  </Page></Guard>;
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
    <Card className="rbac-card"><Form labelAlign="top" colon={false}><Form.FormItem label="版本说明"><Input value={description} onChange={setDescription} /></Form.FormItem></Form></Card>
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
      <Col xs={12} md={6}><Form.FormItem label="目标服务"><Select value={form.service_resource} options={state.services.filter(item => item.isActive).map(item => ({ label: `${item.displayName || item.key} · ${item.key}`, value: item.key }))} onChange={value => setForm({ ...form, service_resource: String(value) })} /></Form.FormItem></Col>
      <Col xs={12} md={6}><Form.FormItem label="Subject"><Input value={form.subject} onChange={value => setForm({ ...form, subject: value })} /></Form.FormItem></Col>
      <Col xs={12} md={6}><Form.FormItem label="Domain"><Input value={form.domain} onChange={value => setForm({ ...form, domain: value })} /></Form.FormItem></Col>
      <Col xs={12} md={6}><Form.FormItem label="HTTP 方法"><Select value={form.method} options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(value => ({ label: value, value }))} onChange={value => setForm({ ...form, method: String(value) })} /></Form.FormItem></Col>
      <Col span={12}><Form.FormItem label="Path"><Input value={form.path} onChange={value => setForm({ ...form, path: value })} /></Form.FormItem></Col>
    </Row></Form></Card>
    <Card className="rbac-card" title="测试结果">{result && typeof result === 'object' ? <Descriptions column={1} bordered items={Object.entries(result).map(([label, value]) => ({ label, content: typeof value === 'boolean' ? <Tag theme={value ? 'success' : 'danger'}>{String(value)}</Tag> : Array.isArray(value) ? value.join('、') || '-' : String(value ?? '-') }))} /> : <Empty text="输入请求后运行测试" />}</Card>
  </Page></Guard>;
};

export default function WorkbenchPage() { const state = useRBAC(); const location = useLocation(); switch (location.pathname) { case '/service-resources': return <Services state={state} />; case '/api-resources': return <APIs state={state} />; case '/roles': return <Roles state={state} />; case '/bindings': return <Users state={state} />; case '/menus': return <Menus state={state} />; case '/releases': return <Releases state={state} />; case '/simulate': return <Simulate state={state} />; default: return <Overview state={state} />; } }
