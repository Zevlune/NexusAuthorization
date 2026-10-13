import { useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { Alert, Button, Card, Checkbox, DialogPlugin, Form, Input, Pagination, Radio, Select, Space, Switch, Table, Tag, type TableProps } from 'tdesign-react';
import { AddIcon, ChevronLeftIcon, DeleteIcon, EditIcon, RefreshIcon, SearchIcon, UploadIcon } from 'tdesign-icons-react';
import {
  createApiPermissionGroup,
  deleteApiPermissionGroup,
  listApiPermissionGroups,
  type ApiPermissionGroup,
  type ApiPermissionGroupAction,
} from '../../../api/api-permission-group';
import type { Endpoint } from '../../../api/rbac-v1';
import { getRequestErrorMessage } from '../../permission-center/shared';
import { Empty, fail, Guard, Page, type State } from '../shared';
import './api-permission-groups.less';

const PAGE_SIZE = 25;
const SUPPORTED = new Set(['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE']);
const READ_METHODS = new Set(['GET', 'HEAD']);
const ACTION_LABEL: Record<ApiPermissionGroupAction, string> = { read: '只读', write: '写入' };
const ACTION_THEME: Record<ApiPermissionGroupAction, 'primary' | 'warning'> = { read: 'primary', write: 'warning' };

const methodTheme = (method: string): 'primary' | 'success' | 'warning' | 'danger' | 'default' => (
  { GET: 'primary', POST: 'success', PUT: 'warning', PATCH: 'warning', DELETE: 'danger' }[method] || 'default'
) as 'primary' | 'success' | 'warning' | 'danger' | 'default';
const endpointTitle = (endpoint: Endpoint) => endpoint.summary || endpoint.endpoint_key || endpoint.path_template;

export default function ApiPermissionGroups({ state }: { state: State }) {
  const { groupId } = useParams();
  const location = useLocation();
  const editing = location.pathname.endsWith('/create') || Boolean(groupId);
  return editing ? <PermissionGroupEditor state={state} groupId={groupId} /> : <PermissionGroupList state={state} />;
}

const serviceOptions = (state: State) => state.services
  .filter(service => service.isActive)
  .map(service => ({ label: `${service.displayName || service.name || service.key} · ${service.key}`, value: service.key }));

const serviceLabel = (state: State, key: string) => {
  const service = state.services.find(item => item.key === key);
  return service ? `${service.displayName || service.name || service.key} · ${service.key}` : key || '未关联服务';
};

const PermissionGroupList = ({ state }: { state: State }) => {
  const navigate = useNavigate();
  const [items, setItems] = useState<ApiPermissionGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [reload, setReload] = useState(0);
  const [keywords, setKeywords] = useState('');
  const [draftKeywords, setDraftKeywords] = useState('');
  const [serviceFilter, setServiceFilter] = useState<string | undefined>(undefined);
  const [page, setPage] = useState(1);
  const appRef = useRef(state.appKey);
  appRef.current = state.appKey;

  useEffect(() => {
    if (!state.appKey) { setItems([]); setLoading(false); return; }
    let cancelled = false;
    setLoading(true); setLoadError('');
    listApiPermissionGroups(state.appKey)
      .then(result => { if (!cancelled) setItems(result.items); })
      .catch(error => { if (!cancelled) setLoadError(getRequestErrorMessage(error, '加载权限组失败')); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [reload, state.appKey]);

  useEffect(() => { setPage(1); setKeywords(''); setDraftKeywords(''); setServiceFilter(undefined); }, [state.appKey]);

  const filtered = useMemo(() => {
    const keyword = keywords.trim().toLowerCase();
    return items.filter(item => {
      const matchesKeyword = !keyword || `${item.code} ${item.name}`.toLowerCase().includes(keyword);
      const matchesService = !serviceFilter || item.service_resource === serviceFilter;
      return matchesKeyword && matchesService;
    });
  }, [items, keywords, serviceFilter]);
  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const displayPage = Math.min(page, pageCount);
  const visible = filtered.slice((displayPage - 1) * PAGE_SIZE, displayPage * PAGE_SIZE);

  const remove = (group: ApiPermissionGroup) => {
    const appKey = state.appKey;
    const dialog = DialogPlugin.confirm({
      header: '删除权限组',
      body: `确认删除“${group.name} · ${group.code}”吗？已被 Token 引用的权限组无法删除，可先停用该权限组使其立即失效。`,
      confirmBtn: { content: '确认删除', theme: 'danger' },
      cancelBtn: '取消',
      onConfirm: async () => {
        try {
          await deleteApiPermissionGroup(appKey, group.id, group.service_resource);
          dialog.destroy();
          if (appRef.current === appKey) setReload(value => value + 1);
        } catch (error) { dialog.destroy(); if (appRef.current === appKey) fail(error, '删除权限组失败'); }
      },
    });
  };

  const columns: TableProps<ApiPermissionGroup>['columns'] = [
    { colKey: 'code', title: '编码', minWidth: 150, cell: ({ row }) => <code>{row.code}</code> },
    { colKey: 'name', title: '名称', minWidth: 150 },
    { colKey: 'service_resource', title: '服务', minWidth: 180, cell: ({ row }) => serviceLabel(state, row.service_resource) },
    { colKey: 'action', title: '类型', width: 100, cell: ({ row }) => <Tag theme={ACTION_THEME[row.action]} variant="light-outline">{ACTION_LABEL[row.action]}</Tag> },
    { colKey: 'endpoint_ids', title: '接口数', width: 90, cell: ({ row }) => row.endpoint_ids.length },
    { colKey: 'enabled', title: '状态', width: 90, cell: ({ row }) => <Tag theme={row.enabled ? 'success' : 'default'} variant="light-outline">{row.enabled ? '启用' : '停用'}</Tag> },
    { colKey: 'actions', title: '操作', width: 190, fixed: 'right', cell: ({ row }) => <Space size={0}>
      <Button variant="text" icon={<EditIcon />} onClick={() => navigate(`/api-permission-groups/${encodeURIComponent(row.id)}/edit`)}>编辑</Button>
      <Button variant="text" theme="danger" icon={<DeleteIcon />} onClick={() => remove(row)}>删除</Button>
    </Space> },
  ];

  return <Guard state={state}><Page title="权限组" actions={<Space>
    <Button variant="outline" icon={<UploadIcon />} onClick={() => navigate('/open-api-permissions')}>导入开放 API</Button>
    <Button variant="outline" icon={<RefreshIcon />} onClick={() => setReload(value => value + 1)}>刷新</Button>
    <Button theme="primary" icon={<AddIcon />} onClick={() => navigate('/api-permission-groups/create')}>创建权限组</Button>
  </Space>}>
    {loadError && <Alert className="api-permission-group-error" theme="error" message={loadError} operation={<Button variant="text" onClick={() => setReload(value => value + 1)}>重试</Button>} />}
    <div className="page-filter-bar"><Form className="workbench-filter-form" layout="inline" colon={false} onSubmit={() => { setPage(1); setKeywords(draftKeywords); }}>
      <Form.FormItem label="关键词"><div className="api-permission-group-control"><Input className="workbench-keyword" value={draftKeywords} clearable placeholder="搜索权限组名称或编码" prefixIcon={<SearchIcon />} onChange={setDraftKeywords} /></div></Form.FormItem>
      <Form.FormItem label="服务"><div className="api-permission-group-control"><Select className="workbench-select" value={serviceFilter} clearable placeholder="全部服务" options={serviceOptions(state)} onChange={value => { setPage(1); setServiceFilter(value ? String(value) : undefined); }} /></div></Form.FormItem>
      <Button type="submit" theme="primary" icon={<SearchIcon />}>查询</Button>
    </Form></div>
    <Card className="rbac-card rbac-table-card"><Table rowKey="id" data={visible} columns={columns} loading={loading || state.loading} hover tableLayout="auto" empty={<Empty text="尚无权限组，可点击“创建权限组”复用一组接口权限" />} /></Card>
    <Pagination className="api-permission-group-pagination" current={displayPage} pageSize={PAGE_SIZE} total={filtered.length} onCurrentChange={setPage} />
  </Page></Guard>;
};

const PermissionGroupEditor = ({ state, groupId }: { state: State; groupId?: string }) => {
  const navigate = useNavigate();
  const [group, setGroup] = useState<ApiPermissionGroup | null>(null);
  const [service, setService] = useState('');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [groupAction, setGroupAction] = useState<ApiPermissionGroupAction>('read');
  const [enabled, setEnabled] = useState(true);
  const [endpointIds, setEndpointIds] = useState<string[]>([]);
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [reload, setReload] = useState(0);
  const appRef = useRef(state.appKey);
  appRef.current = state.appKey;
  const activeServices = useMemo(() => state.services.filter(item => item.isActive), [state.services]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true); setLoadError('');
    const request = groupId ? listApiPermissionGroups(state.appKey) : Promise.resolve({ items: [] as ApiPermissionGroup[] });
    request.then(result => {
      if (cancelled) return;
      if (!groupId) { setService(current => current || state.services.find(item => item.isActive)?.key || ''); return; }
      const found = result.items.find(item => item.id === groupId);
      if (!found) { setLoadError('未找到该权限组，可能已被删除'); return; }
      setGroup(found); setService(found.service_resource); setCode(found.code); setName(found.name); setGroupAction(found.action); setEnabled(found.enabled); setEndpointIds(found.endpoint_ids);
    }).catch(error => { if (!cancelled) setLoadError(getRequestErrorMessage(error, '加载权限组失败')); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [reload, state.appKey, state.services, groupId]);

  const serviceEndpoints = useMemo(() => state.endpoints.filter(endpoint => endpoint.enabled && endpoint.service_resource === service && SUPPORTED.has(endpoint.method)), [state.endpoints, service]);
  const allowedEndpoints = useMemo(() => groupAction === 'read' ? serviceEndpoints.filter(endpoint => READ_METHODS.has(endpoint.method)) : serviceEndpoints, [serviceEndpoints, groupAction]);
  const endpointByID = useMemo(() => new Map(state.endpoints.map(endpoint => [endpoint.id, endpoint])), [state.endpoints]);
  const unavailableEndpoints = endpointIds.filter(id => {
    const endpoint = endpointByID.get(id);
    return !endpoint || !endpoint.enabled || endpoint.service_resource !== service || !SUPPORTED.has(endpoint.method) || (groupAction === 'read' && !READ_METHODS.has(endpoint.method));
  });
  const keyword = search.trim().toLowerCase();
  const filtered = allowedEndpoints.filter(endpoint => !keyword || `${endpointTitle(endpoint)} ${endpoint.method} ${endpoint.path_template} ${endpoint.module}`.toLowerCase().includes(keyword));
  const toggleEndpoints = (values: string[]) => setEndpointIds(current => [
    ...current.filter(id => !filtered.some(endpoint => endpoint.id === id)),
    ...filtered.filter(endpoint => values.includes(endpoint.id)).map(endpoint => endpoint.id),
  ]);
  const changeAction = (next: ApiPermissionGroupAction) => {
    setGroupAction(next);
    if (next === 'read') setEndpointIds(current => current.filter(id => serviceEndpoints.some(endpoint => endpoint.id === id && READ_METHODS.has(endpoint.method))));
  };

  const submit = async () => {
    if (saving) return;
    setError('');
    if (!service) { setError('请选择服务'); return; }
    if (!code.trim() || !name.trim()) { setError('请填写权限组编码和名称'); return; }
    if (!activeServices.some(item => item.key === service)) { setError('服务未关联或已停用，请重新选择'); return; }
    if (!endpointIds.length) { setError('请至少选择一个接口'); return; }
    if (endpointIds.length > 1000) { setError('最多选择 1000 个接口'); return; }
    if (unavailableEndpoints.length) { setError('已选接口包含停用、跨服务或不兼容当前权限类型的接口，请先移除后再保存'); return; }
    const appKey = state.appKey;
    setSaving(true);
    try {
      await createApiPermissionGroup(appKey, service, { id: group?.id, service_resource: service, code: code.trim(), name: name.trim(), action: groupAction, enabled, endpoint_ids: endpointIds });
      if (appRef.current === appKey) navigate('/api-permission-groups');
    } catch (err) { if (appRef.current === appKey) setError(getRequestErrorMessage(err, '保存权限组失败')); }
    finally { if (appRef.current === appKey) setSaving(false); }
  };

  if (loading || state.loading) return <Page><Empty text="正在加载权限组…" /></Page>;
  if (loadError) return <Page><Alert theme="error" message={loadError} operation={<Space><Button variant="text" onClick={() => setReload(value => value + 1)}>重试</Button><Button variant="text" onClick={() => navigate('/api-permission-groups')}>返回列表</Button></Space>} /></Page>;

  return <Guard state={state}><Page actions={<Button variant="outline" icon={<ChevronLeftIcon />} disabled={saving} onClick={() => navigate('/api-permission-groups')}>返回权限组</Button>}>
    <div className="api-permission-group-heading"><h2>{group ? '编辑权限组' : '创建权限组'}</h2><p>复用一组接口权限：在 Token 中选择服务后勾选权限组即可开放对应接口。权限组的改动会立即作用于引用它的 Token。</p></div>
    {error && <Alert className="api-permission-group-error" theme="error" message={error} />}
    <Card className="api-permission-group-card" title="基础信息">
      <Form labelAlign="top" colon={false}><div className="api-permission-group-grid">
        <Form.FormItem label="服务" requiredMark help={group ? '创建后不可更改。' : undefined}><div className="api-permission-group-control"><Select aria-label="服务" value={service} disabled={Boolean(group) || saving} placeholder="请选择服务" options={serviceOptions(state)} onChange={value => { setService(String(value)); setEndpointIds([]); setSearch(''); }} /></div></Form.FormItem>
        <Form.FormItem label="权限组编码" requiredMark help={group ? '创建后不可更改。' : undefined}><div className="api-permission-group-control"><Input aria-label="权限组编码" value={code} disabled={Boolean(group) || saving} maxlength={120} placeholder="例如：orders.read" onChange={setCode} /></div></Form.FormItem>
        <Form.FormItem label="显示名称" requiredMark><div className="api-permission-group-control"><Input aria-label="显示名称" value={name} disabled={saving} maxlength={200} placeholder="例如：订单只读" onChange={setName} /></div></Form.FormItem>
        <Form.FormItem label="权限类型" requiredMark><div className="api-permission-group-control"><Radio.Group variant="outline" value={groupAction} disabled={saving} onChange={value => changeAction(value as ApiPermissionGroupAction)}><Radio.Button value="read">只读</Radio.Button><Radio.Button value="write">写入</Radio.Button></Radio.Group></div></Form.FormItem>
        <Form.FormItem label="启用"><div className="api-permission-group-control api-permission-group-switch"><Switch value={enabled} disabled={saving} onChange={value => setEnabled(Boolean(value))} /></div></Form.FormItem>
      </div></Form>
    </Card>
    <Card className="api-permission-group-card" title={`接口 · 已选择 ${endpointIds.length} 个`} actions={<Button variant="text" disabled={saving} onClick={() => navigate('/open-api-permissions')}>维护开放 API 接口</Button>}>
      <p className="api-permission-group-note">{groupAction === 'read' ? '只读权限组仅能勾选 GET / HEAD 接口。' : '写入权限组可勾选任意受支持的接口，包括 GET / HEAD。'}</p>
      <div className="api-permission-group-toolbar"><Input aria-label="搜索接口" value={search} clearable placeholder="搜索接口名称、模块或路径" onChange={setSearch} /><Space><Button variant="outline" disabled={saving || !filtered.length} onClick={() => toggleEndpoints(filtered.map(endpoint => endpoint.id))}>全选当前</Button><Button variant="text" disabled={saving || !endpointIds.length} onClick={() => setEndpointIds([])}>清空</Button></Space></div>
      {unavailableEndpoints.length > 0 && <div className="api-permission-group-unavailable"><Alert theme="warning" message="以下已选接口已停用、跨服务或不兼容当前权限类型，请先移除后再保存。" />{unavailableEndpoints.map(id => { const endpoint = endpointByID.get(id); return <div key={id}><span>{endpoint ? `${endpointTitle(endpoint)} · ${endpoint.method} ${endpoint.path_template}` : id}</span><Button variant="text" theme="danger" disabled={saving} onClick={() => setEndpointIds(current => current.filter(item => item !== id))}>移除接口</Button></div>; })}</div>}
      {!service && <Empty text="请先选择服务" />}
      {service && !allowedEndpoints.length && <Empty text="该服务尚无可授权接口，请先在开放 API 权限中导入或新增" />}
      {service && Boolean(allowedEndpoints.length) && <Checkbox.Group className="api-permission-group-endpoints" value={endpointIds} disabled={saving} onChange={values => toggleEndpoints(values.map(String))}>
        {filtered.map(endpoint => <Checkbox key={endpoint.id} value={endpoint.id}><span className="api-permission-group-endpoint"><strong>{endpointTitle(endpoint)}</strong><span><Tag theme={methodTheme(endpoint.method)} variant="light">{endpoint.method}</Tag><code>{endpoint.path_template}</code><small>{endpoint.module || '未分类'}</small></span></span></Checkbox>)}
      </Checkbox.Group>}
      {service && Boolean(allowedEndpoints.length) && !filtered.length && <Empty text="没有匹配的接口" />}
    </Card>
    <div className="api-permission-group-footer"><span>{groupAction === 'read' ? '只读' : '写入'} · 已选择 {endpointIds.length} 个接口</span><Space><Button variant="outline" disabled={saving} onClick={() => navigate('/api-permission-groups')}>取消</Button><Button theme="primary" loading={saving} disabled={saving || Boolean(state.loadError)} onClick={() => void submit()}>{group ? '保存修改' : '创建权限组'}</Button></Space></div>
  </Page></Guard>;
};

