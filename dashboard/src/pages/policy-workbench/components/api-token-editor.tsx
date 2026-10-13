import { useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { Alert, Button, Card, Checkbox, Form, Input, Radio, Select, Space, Tag } from 'tdesign-react';
import { AddIcon, DeleteIcon, EditIcon, ChevronLeftIcon, SecuredIcon } from 'tdesign-icons-react';
import { createApiToken, getApiToken, updateApiToken, type ApiToken, type ApiTokenPayload, type ApiTokenPermission, type ApiTokenSecret } from '../../../api/api-token';
import { listApiPermissionGroups, type ApiPermissionGroup, type ApiPermissionGroupAction } from '../../../api/api-permission-group';
import type { Endpoint } from '../../../api/rbac-v1';
import { getRequestErrorMessage } from '../../permission-center/shared';
import { Empty, Guard, Page, type State } from '../shared';
import { RawTokenDialog } from './api-tokens';
import './api-token-editor.less';

type Expiry = 'permanent' | '30' | '180' | 'custom';
type LegacyPolicy = { id: string; service: string; tenant: string; project: string; permissions: ApiTokenPermission[] };
type GroupFilter = 'all' | ApiPermissionGroupAction;
type GroupPolicy = { id: string; service: string; tenant: string; project: string; groupIDs: string[]; filter: GroupFilter };

const supported = new Set(['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE']);
const readMethods = new Set(['GET', 'HEAD']);
const action = (method: string): 'read' | 'write' => readMethods.has(method) ? 'read' : 'write';
const endpointName = (endpoint: Endpoint) => endpoint.summary || endpoint.endpoint_key || endpoint.path_template;
const groupName = (group: ApiPermissionGroup) => `${group.name} · ${group.code}`;
const policyID = () => crypto.randomUUID();
const FILTER_OPTIONS = [{ label: '全部', value: 'all' }, { label: '只读', value: 'read' }, { label: '写入', value: 'write' }];

// 历史 Token 仅有 permissions，需沿用既有的按服务/范围分组逻辑原样编辑。
const legacyGroupPolicies = (permissions: ApiTokenPermission[]): LegacyPolicy[] => {
  const groups = new Map<string, LegacyPolicy>();
  permissions.forEach(permission => {
    const key = JSON.stringify([permission.service_resource, permission.tenant_id, permission.project_id]);
    if (!groups.has(key)) groups.set(key, { id: policyID(), service: permission.service_resource, tenant: permission.tenant_id, project: permission.project_id, permissions: [] });
    groups.get(key)!.permissions.push(permission);
  });
  return [...groups.values()];
};

export default function ApiTokenEditor({ state }: { state: State }) {
  const navigate = useNavigate();
  const { tokenId } = useParams();
  const location = useLocation();
  const permissionsOnly = location.pathname.endsWith('/permissions');
  const [token, setToken] = useState<ApiToken | null>(null);
  const [groups, setGroups] = useState<ApiPermissionGroup[]>([]);
  const [name, setName] = useState('');
  const [expiry, setExpiry] = useState<Expiry>('permanent');
  const [customDate, setCustomDate] = useState('');
  const [legacyPolicies, setLegacyPolicies] = useState<LegacyPolicy[]>([]);
  const [groupPolicies, setGroupPolicies] = useState<GroupPolicy[]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [secret, setSecret] = useState<ApiTokenSecret | null>(null);
  const [reload, setReload] = useState(0);
  const appRef = useRef(state.appKey);
  appRef.current = state.appKey;

  useEffect(() => {
    if (!state.appKey) { setLoading(false); return; }
    let cancelled = false;
    setLoading(true); setLoadError('');
    Promise.all([
      tokenId ? getApiToken(state.appKey, tokenId) : Promise.resolve(null),
      listApiPermissionGroups(state.appKey),
    ]).then(([existing, groupResult]) => {
      if (cancelled) return;
      setGroups(groupResult.items);
      if (!existing) return;
      setToken(existing); setName(existing.name);
      setExpiry(existing.expires_at ? 'custom' : 'permanent'); setCustomDate(existing.expires_at?.slice(0, 10) || '');
      if (existing.policies.length) setGroupPolicies(existing.policies.map(policy => ({ id: policy.id, service: policy.service_resource, tenant: policy.tenant_id, project: policy.project_id, groupIDs: policy.permission_group_ids, filter: 'all' })));
      else setLegacyPolicies(legacyGroupPolicies(existing.permissions));
    }).catch(err => { if (!cancelled) setLoadError(getRequestErrorMessage(err, '加载 Token 和权限组失败')); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [reload, state.appKey, tokenId]);

  const legacy = Boolean(token && token.policies.length === 0 && token.permissions.length > 0);
  const services = state.services.filter(service => service.isActive);
  const endpoints = useMemo(() => state.endpoints.filter(endpoint => endpoint.enabled && supported.has(endpoint.method)), [state.endpoints]);
  const byID = useMemo(() => new Map(state.endpoints.map(endpoint => [endpoint.id, endpoint])), [state.endpoints]);
  const groupByID = useMemo(() => new Map(groups.map(group => [group.id, group])), [groups]);
  const serviceName = (key: string) => state.services.find(service => service.key === key)?.displayName || key;
  const allLegacyPermissions = legacyPolicies.flatMap(policy => policy.permissions.map(permission => ({ ...permission, service_resource: policy.service, tenant_id: policy.tenant.trim(), project_id: policy.project.trim() })));

  const updateLegacyPolicy = (id: string, patch: Partial<LegacyPolicy>) => setLegacyPolicies(current => current.map(policy => policy.id === id ? { ...policy, ...patch } : policy));
  const updateGroupPolicy = (id: string, patch: Partial<GroupPolicy>) => setGroupPolicies(current => current.map(policy => policy.id === id ? { ...policy, ...patch } : policy));
  const removeLegacyPolicy = (id: string) => setLegacyPolicies(current => current.filter(policy => policy.id !== id));
  const removeGroupPolicy = (id: string) => setGroupPolicies(current => current.filter(policy => policy.id !== id));
  const makePermission = (endpoint: Endpoint): ApiTokenPermission => ({ endpoint_id: endpoint.id, service_resource: endpoint.service_resource, action: action(endpoint.method), tenant_id: '', project_id: '', method: endpoint.method, path_template: endpoint.path_template, summary: endpoint.summary });
  const addLegacyPolicy = (service: string, mode?: 'read' | 'write' | 'all') => {
    const policy: LegacyPolicy = { id: policyID(), service, tenant: '', project: '', permissions: mode ? endpoints.filter(endpoint => endpoint.service_resource === service && (mode === 'all' || action(endpoint.method) === mode)).map(makePermission) : [] };
    setLegacyPolicies(current => [...current, policy]); setEditing(policy.id); setSearch('');
  };
  const addGroupPolicy = (service: string, filter: GroupFilter = 'all') => {
    const policy: GroupPolicy = { id: policyID(), service, tenant: '', project: '', groupIDs: [], filter };
    setGroupPolicies(current => [...current, policy]); setEditing(policy.id);
  };

  const expireAt = (): string | null | undefined => {
    if (expiry === 'custom') {
      if (!customDate) { setError('请选择过期日期'); return undefined; }
      return token?.expires_at?.slice(0, 10) === customDate ? token.expires_at : new Date(`${customDate}T23:59:59`).toISOString();
    }
    if (expiry !== 'permanent') return new Date(Date.now() + Number(expiry) * 86400000).toISOString();
    return null;
  };

  const submit = async () => {
    if (saving) return;
    setError('');
    if (!name.trim()) { setError('请输入 Token 名称'); return; }
    if (!legacy && (!groupPolicies.length || groupPolicies.some(policy => !policy.service || !policy.groupIDs.length))) { setError('每条权限策略需要选择服务和至少一个权限组'); return; }
    if (!legacy && groupPolicies.length > 100) { setError('最多配置 100 条权限策略'); return; }
    if (!legacy && groupPolicies.some(policy => !services.some(service => service.key === policy.service))) { setError('权限策略包含未关联或停用的服务，请移除后重新选择'); return; }
    if (!legacy && groupPolicies.some(policy => policy.groupIDs.length > 1000)) { setError('每条策略最多选择 1000 个权限组'); return; }
    if (!legacy && groupPolicies.some(policy => policy.groupIDs.some(id => { const group = groupByID.get(id); return !group || !group.enabled || group.service_resource !== policy.service; }))) { setError('权限策略包含已停用、跨服务或不存在的权限组，请先移除后再保存'); return; }
    if (legacy && (!legacyPolicies.length || legacyPolicies.some(policy => !policy.service || !policy.permissions.length))) { setError('每条权限策略需要选择服务和至少一个 API 权限'); return; }
    if (legacy) {
      const keys = allLegacyPermissions.map(permission => JSON.stringify([permission.endpoint_id, permission.tenant_id, permission.project_id]));
      if (new Set(keys).size !== keys.length) { setError('相同服务和范围下存在重复的 API 权限，请合并策略或调整范围'); return; }
      if (allLegacyPermissions.length > 1000) { setError('最多选择 1000 项 API 权限'); return; }
      if (allLegacyPermissions.some(permission => { const endpoint = byID.get(permission.endpoint_id); return !endpoint?.enabled || endpoint.service_resource !== permission.service_resource || !supported.has(endpoint.method); })) { setError('已选权限包含停用或已移除的接口，请先移除这些权限'); return; }
    }
    const expiresAt = expireAt();
    if (expiresAt === undefined) return;
    if (expiresAt && Date.parse(expiresAt) <= Date.now()) { setError('过期时间必须在未来'); return; }
    const payload: ApiTokenPayload = legacy
      ? { name: name.trim(), expires_at: expiresAt, owner_type: token?.owner_type || 'service', owner_id: token?.owner_id || '', permissions: allLegacyPermissions.map(permission => ({ ...permission, action: action(byID.get(permission.endpoint_id)!.method) })) }
      : { name: name.trim(), expires_at: expiresAt, policies: groupPolicies.map(policy => ({ id: policy.id, effect: 'allow', service_resource: policy.service, tenant_id: policy.tenant.trim(), project_id: policy.project.trim(), permission_group_ids: policy.groupIDs })) };
    const appKey = state.appKey;
    setSaving(true);
    try {
      if (tokenId) { await updateApiToken(appKey, tokenId, payload); if (appRef.current === appKey) navigate('/api-tokens'); }
      else { const result = await createApiToken(appKey, payload); if (appRef.current === appKey) setSecret(result); }
    } catch (err) { if (appRef.current === appKey) setError(getRequestErrorMessage(err, '保存 Token 失败')); }
    finally { if (appRef.current === appKey) setSaving(false); }
  };

  if (loading || state.loading) return <Page><Empty text="正在加载 Token 配置…" /></Page>;
  if (loadError) return <Page><Alert theme="error" message={loadError} operation={<Button variant="text" onClick={() => setReload(value => value + 1)}>重试</Button>} /></Page>;
  if (token?.status === 'revoked') return <Page><Alert theme="warning" message="已撤销的 Token 无法修改" /><Button onClick={() => navigate('/api-tokens')}>返回列表</Button></Page>;

  const heading = legacy ? (permissionsOnly ? '维护权限策略' : '编辑 API Token') : permissionsOnly ? '维护权限策略' : tokenId ? '编辑 API Token' : '创建服务 API Token';
  const availableGroups = (policy: GroupPolicy) => groups.filter(group => group.enabled && group.service_resource === policy.service && (policy.filter === 'all' || group.action === policy.filter));
  const unavailableGroupIDs = (policy: GroupPolicy) => policy.groupIDs.filter(id => { const group = groupByID.get(id); return !group || !group.enabled || group.service_resource !== policy.service; });
  const applyGroupSelection = (policy: GroupPolicy, available: ApiPermissionGroup[], values: string[]) => {
    const visible = new Set(available.map(group => group.id));
    const kept = policy.groupIDs.filter(id => !visible.has(id));
    updateGroupPolicy(policy.id, { groupIDs: [...kept, ...values.filter(id => visible.has(id))] });
  };

  return <Guard state={state}><Page actions={<Button variant="outline" icon={<ChevronLeftIcon />} disabled={saving} onClick={() => navigate('/api-tokens')}>返回 Token 列表</Button>}>
    <div className="token-editor-heading"><h2>{heading}</h2><p>{legacy ? '该 Token 使用历史接口权限，保留原有的显式接口配置，可继续在此维护。' : '为调用方服务分配可复用的权限组，权限组的接口变化会立即作用于本 Token。'}</p></div>
    {error && <Alert className="api-token-error" theme="error" message={error} />}
    <Card className="token-editor-card" title="Token 信息">
      <Form labelAlign="top" colon={false}><div className="token-editor-grid">
        <Form.FormItem label="Token 名称" requiredMark><div className="token-editor-control"><Input value={name} disabled={saving || permissionsOnly} placeholder="例如：订单服务生产 Token" maxlength={200} onChange={setName} /></div></Form.FormItem>
        <Form.FormItem label="Token 类型"><div className="token-editor-fixed-type"><Tag theme="primary" variant="light">{token?.owner_type === 'user' ? '历史用户 Token' : '服务类型'}</Tag></div></Form.FormItem>
        <Form.FormItem label="有效期"><div className="token-editor-expiry"><Select value={expiry} disabled={saving || permissionsOnly} options={[{ label: '永久', value: 'permanent' }, { label: '30 天', value: '30' }, { label: '180 天', value: '180' }, { label: '自定义日期', value: 'custom' }]} onChange={value => setExpiry(value as Expiry)} />{expiry === 'custom' && <input className="api-token-date" type="date" aria-label="自定义过期日期" disabled={saving || permissionsOnly} value={customDate} onChange={event => setCustomDate(event.target.value)} />}</div></Form.FormItem>
      </div></Form>
    </Card>
    <Card className="token-editor-card" title="Permission policies · 权限策略" actions={<Space><Button variant="text" disabled={saving} onClick={() => navigate('/api-permission-groups')}>管理权限组</Button><Button variant="text" disabled={saving} onClick={() => navigate('/open-api-permissions')}>维护开放 API 权限</Button></Space>}>
      {legacy
        ? <LegacyPolicyEditor saving={saving} policies={legacyPolicies} services={services} endpoints={endpoints} serviceName={serviceName} byID={byID} search={search} setSearch={setSearch} editing={editing} setEditing={setEditing} addPolicy={addLegacyPolicy} updatePolicy={updateLegacyPolicy} removePolicy={removeLegacyPolicy} />
        : <GroupPolicyEditor saving={saving} policies={groupPolicies} services={services} serviceName={serviceName} groups={groups} groupByID={groupByID} availableGroups={availableGroups} unavailableGroupIDs={unavailableGroupIDs} applyGroupSelection={applyGroupSelection} editing={editing} setEditing={setEditing} addPolicy={addGroupPolicy} updatePolicy={updateGroupPolicy} removePolicy={removeGroupPolicy} />}
    </Card>
    <div className="token-editor-footer"><span>{legacy ? `已配置 ${legacyPolicies.length} 条策略，共 ${allLegacyPermissions.length} 项 API 权限` : `已配置 ${groupPolicies.length} 条策略，共 ${groupPolicies.reduce((sum, policy) => sum + policy.groupIDs.length, 0)} 个权限组`}</span><Space><Button variant="outline" disabled={saving} onClick={() => navigate('/api-tokens')}>取消</Button><Button theme="primary" loading={saving} disabled={saving || Boolean(secret) || Boolean(state.loadError)} onClick={() => void submit()}>{permissionsOnly ? '保存权限策略' : tokenId ? '保存修改' : '创建 Token'}</Button></Space></div>
    <RawTokenDialog secret={secret} onClose={() => { setSecret(null); navigate('/api-tokens'); }} />
  </Page></Guard>;
}

type GroupPolicyEditorProps = {
  saving: boolean;
  policies: GroupPolicy[];
  services: State['services'];
  serviceName: (key: string) => string;
  groups: ApiPermissionGroup[];
  groupByID: Map<string, ApiPermissionGroup>;
  availableGroups: (policy: GroupPolicy) => ApiPermissionGroup[];
  unavailableGroupIDs: (policy: GroupPolicy) => string[];
  applyGroupSelection: (policy: GroupPolicy, available: ApiPermissionGroup[], values: string[]) => void;
  editing: string | null;
  setEditing: (id: string | null) => void;
  addPolicy: (service: string, filter?: GroupFilter) => void;
  updatePolicy: (id: string, patch: Partial<GroupPolicy>) => void;
  removePolicy: (id: string) => void;
};

const GroupPolicyEditor = ({ saving, policies, services, serviceName, groups, groupByID, availableGroups, unavailableGroupIDs, applyGroupSelection, editing, setEditing, addPolicy, updatePolicy, removePolicy }: GroupPolicyEditorProps) => {
  const navigate = useNavigate();
  return <>
    <p className="token-editor-note">选择目标服务并在该服务下勾选权限组。权限组的接口配置可被多个 Token 复用，权限组的改动会立即作用于引用它的 Token。</p>
    {!services.length && <div className="token-editor-empty"><Empty text="当前应用尚未关联启用的服务，请先在服务资源中关联服务" /><Button variant="outline" onClick={() => navigate('/service-resources')}>前往关联服务</Button></div>}
    {Boolean(services.length) && !groups.length && !policies.length && <div className="token-editor-empty"><Empty text="当前应用尚无权限组，请先创建权限组后再为 Token 授权" /><Space><Button variant="outline" onClick={() => navigate('/open-api-permissions')}>维护开放 API 接口</Button><Button theme="primary" onClick={() => navigate('/api-permission-groups/create')}>创建权限组</Button></Space></div>}
    {Boolean(services.length) && (groups.length > 0 || policies.length > 0) && <>
    <div className="token-policy-templates">
      <button type="button" className="token-policy-template" disabled={saving || !services.length} onClick={() => addPolicy(services[0]?.key || '')}><AddIcon /><span><strong>从零配置 <Tag variant="light">自定义</Tag></strong><small>选择服务、租户/项目范围和权限组</small></span></button>
      {services.map(service => { const available = groups.filter(group => group.service_resource === service.key); return <div className="token-policy-template" key={service.key}><SecuredIcon /><span><strong>{serviceName(service.key)}</strong><small>{available.length} 个可用权限组 · {service.key}</small><Space size={4}><Button variant="text" size="small" disabled={saving} onClick={() => addPolicy(service.key, 'all')}>添加策略</Button><Button variant="text" size="small" disabled={saving || !available.some(group => group.action === 'read')} onClick={() => addPolicy(service.key, 'read')}>只读</Button><Button variant="text" size="small" disabled={saving || !available.some(group => group.action === 'write')} onClick={() => addPolicy(service.key, 'write')}>写入</Button></Space></span></div>; })}
    </div>
    <div className="token-policy-list">{policies.map((policy, index) => {
      const isEditing = editing === policy.id;
      const available = availableGroups(policy);
      const unavailable = unavailableGroupIDs(policy);
      const selectedCount = policy.groupIDs.length;
      return <section className="token-policy" key={policy.id}>
        <div className="token-policy-header"><div><strong>策略 {index + 1} · {serviceName(policy.service) || '选择目标服务'}</strong><small>{selectedCount} 个权限组{unavailable.length ? ` · ${unavailable.length} 个不可用` : ''} · {policy.tenant || '全部租户'} / {policy.project || '全部项目'}</small></div><Space><Button aria-label={`编辑策略 ${index + 1}`} variant="text" icon={<EditIcon />} disabled={saving} onClick={() => setEditing(isEditing ? null : policy.id)}>{isEditing ? '收起' : '编辑'}</Button><Button aria-label={`删除策略 ${index + 1}`} variant="text" theme="danger" icon={<DeleteIcon />} disabled={saving} onClick={() => removePolicy(policy.id)}>删除</Button></Space></div>
        {!isEditing && <p className="token-policy-summary">{selectedCount ? policy.groupIDs.map(id => { const group = groupByID.get(id); return group ? groupName(group) : id; }).join('、') : '尚未选择权限组'}</p>}
        {isEditing && <div className="token-policy-body">
          <Form labelAlign="top" colon={false}><div className="token-policy-scope">
            <Form.FormItem label="目标服务" requiredMark><div className="token-editor-control"><Select aria-label={`策略 ${index + 1} 目标服务`} value={policy.service} disabled={saving} options={services.map(service => ({ label: `${serviceName(service.key)} · ${service.key}`, value: service.key }))} onChange={value => updatePolicy(policy.id, { service: String(value), groupIDs: [] })} /></div></Form.FormItem>
            <Form.FormItem label="租户范围"><div className="token-editor-control"><Input aria-label={`策略 ${index + 1} 租户范围`} value={policy.tenant} disabled={saving} placeholder="留空表示全部租户" maxlength={200} onChange={value => updatePolicy(policy.id, { tenant: value })} /></div></Form.FormItem>
            <Form.FormItem label="项目范围"><div className="token-editor-control"><Input aria-label={`策略 ${index + 1} 项目范围`} value={policy.project} disabled={saving} placeholder="留空表示全部项目" maxlength={200} onChange={value => updatePolicy(policy.id, { project: value })} /></div></Form.FormItem>
          </div></Form>
          <div className="token-policy-toolbar"><Radio.Group variant="outline" value={policy.filter} disabled={saving} onChange={value => updatePolicy(policy.id, { filter: value as GroupFilter })}>{FILTER_OPTIONS.map(option => <Radio.Button key={option.value} value={option.value}>{option.label}</Radio.Button>)}</Radio.Group><span className="token-policy-count">已选 {selectedCount} 个权限组</span></div>
          {unavailable.length > 0 && <div className="token-policy-unavailable"><Alert theme="warning" message="以下已选权限组已停用、跨服务或不存在，请移除后再保存。" />{unavailable.map(id => { const group = groupByID.get(id); return <div key={id}><span>{group ? groupName(group) : id}</span><Button variant="text" theme="danger" disabled={saving} onClick={() => updatePolicy(policy.id, { groupIDs: policy.groupIDs.filter(item => item !== id) })}>移除权限组</Button></div>; })}</div>}
          {!available.length && <div className="token-editor-empty"><Empty text={groups.some(group => group.enabled && group.service_resource === policy.service) ? '当前筛选下没有启用权限组' : '该服务暂无启用权限组'} /><Button variant="outline" onClick={() => navigate('/api-permission-groups/create')}>新建权限组</Button></div>}
          {Boolean(available.length) && <Checkbox.Group className="token-policy-groups" value={policy.groupIDs} disabled={saving} onChange={values => applyGroupSelection(policy, available, values.map(String))}>
            {available.map(group => <Checkbox key={group.id} value={group.id}><span className="token-policy-group"><strong>{groupName(group)}</strong><span><Tag theme={group.action === 'read' ? 'primary' : 'warning'} variant="light">{group.action === 'read' ? '只读' : '写入'}</Tag><small>{group.endpoint_ids.length} 个接口</small>{!group.enabled ? <Tag theme="default" variant="light">已停用</Tag> : null}</span></span></Checkbox>)}
          </Checkbox.Group>}
        </div>}
      </section>;
    })}</div>
    </>}
  </>;
};

type LegacyPolicyEditorProps = {
  saving: boolean;
  policies: LegacyPolicy[];
  services: State['services'];
  endpoints: Endpoint[];
  serviceName: (key: string) => string;
  byID: Map<string, Endpoint>;
  search: string;
  setSearch: (value: string) => void;
  editing: string | null;
  setEditing: (id: string | null) => void;
  addPolicy: (service: string, mode?: 'read' | 'write' | 'all') => void;
  updatePolicy: (id: string, patch: Partial<LegacyPolicy>) => void;
  removePolicy: (id: string) => void;
};

const LegacyPolicyEditor = ({ saving, policies, services, endpoints, serviceName, byID, search, setSearch, editing, setEditing, addPolicy, updatePolicy, removePolicy }: LegacyPolicyEditorProps) => {
  const navigate = useNavigate();
  const makePermission = (endpoint: Endpoint): ApiTokenPermission => ({ endpoint_id: endpoint.id, service_resource: endpoint.service_resource, action: action(endpoint.method), tenant_id: '', project_id: '', method: endpoint.method, path_template: endpoint.path_template, summary: endpoint.summary });
  return <>
    <Alert className="token-legacy-notice" theme="info" message="该 Token 使用历史接口权限（permissions），这里保留显式接口编辑，保存时不会自动转换为权限组。" />
    <p className="token-editor-note">仅可选择当前应用关联服务中的启用接口。批量选择保存为具体接口权限，后续新接口需要重新授权。</p>
    <div className="token-policy-templates">
      <button type="button" className="token-policy-template" disabled={saving || !services.length} onClick={() => addPolicy(services[0]?.key || '')}><AddIcon /><span><strong>从零配置 <Tag variant="light">自定义</Tag></strong><small>选择服务、API 接口和访问范围</small></span></button>
      {services.map(service => { const available = endpoints.filter(endpoint => endpoint.service_resource === service.key); return <div className="token-policy-template" key={service.key}><SecuredIcon /><span><strong>{serviceName(service.key)}</strong><small>{available.length} 项 API 权限 · {service.key}</small><Space size={4}><Button variant="text" size="small" disabled={saving || !available.length} onClick={() => addPolicy(service.key, 'all')}>选择全部</Button><Button variant="text" size="small" disabled={saving || !available.some(endpoint => action(endpoint.method) === 'read')} onClick={() => addPolicy(service.key, 'read')}>只读权限</Button><Button variant="text" size="small" disabled={saving || !available.some(endpoint => action(endpoint.method) === 'write')} onClick={() => addPolicy(service.key, 'write')}>写入权限</Button></Space></span></div>; })}
    </div>
    {!services.length && <div className="token-editor-empty"><Empty text="当前应用尚未关联启用的服务，请先在服务资源中关联服务" /><Button variant="outline" onClick={() => navigate('/service-resources')}>前往关联服务</Button></div>}
    <div className="token-policy-list">{policies.map((policy, index) => {
      const isEditing = editing === policy.id;
      const available = endpoints.filter(endpoint => endpoint.service_resource === policy.service);
      const filtered = available.filter(endpoint => `${endpointName(endpoint)} ${endpoint.method} ${endpoint.path_template} ${endpoint.module}`.toLowerCase().includes(search.toLowerCase()));
      const selected = new Set(policy.permissions.map(permission => permission.endpoint_id));
      const unavailable = policy.permissions.filter(permission => !available.some(endpoint => endpoint.id === permission.endpoint_id));
      const setSelected = (values: string[]) => updatePolicy(policy.id, { permissions: [...policy.permissions.filter(permission => !filtered.some(endpoint => endpoint.id === permission.endpoint_id)), ...filtered.filter(endpoint => values.includes(endpoint.id)).map(makePermission)] });
      const bulk = (mode: 'read' | 'write' | 'all') => { const batch = available.filter(endpoint => mode === 'all' || action(endpoint.method) === mode); updatePolicy(policy.id, { permissions: [...policy.permissions, ...batch.filter(endpoint => !selected.has(endpoint.id)).map(makePermission)] }); };
      return <section className="token-policy" key={policy.id}>
        <div className="token-policy-header"><div><strong>策略 {index + 1} · {serviceName(policy.service) || '选择目标服务'}</strong><small>{policy.permissions.length} 项权限 · {policy.tenant || '全部租户'} / {policy.project || '全部项目'}</small></div><Space><Button aria-label={`编辑策略 ${index + 1}`} variant="text" icon={<EditIcon />} disabled={saving} onClick={() => { setEditing(isEditing ? null : policy.id); setSearch(''); }}>{isEditing ? '收起' : '编辑'}</Button><Button aria-label={`删除策略 ${index + 1}`} variant="text" theme="danger" icon={<DeleteIcon />} disabled={saving} onClick={() => removePolicy(policy.id)}>删除</Button></Space></div>
        {!isEditing && <p className="token-policy-summary">{policy.permissions.slice(0, 4).map(permission => permission.summary || byID.get(permission.endpoint_id)?.summary || permission.path_template || permission.endpoint_id).join('、') || '尚未选择权限'}{policy.permissions.length > 4 ? ` 等 ${policy.permissions.length} 项` : ''}</p>}
        {isEditing && <div className="token-policy-body">
          <Form labelAlign="top" colon={false}><div className="token-policy-scope">
            <Form.FormItem label="目标服务" requiredMark><div className="token-editor-control"><Select aria-label={`策略 ${index + 1} 目标服务`} value={policy.service} disabled={saving} options={services.map(service => ({ label: `${serviceName(service.key)} · ${service.key}`, value: service.key }))} onChange={value => { updatePolicy(policy.id, { service: String(value), permissions: [] }); setSearch(''); }} /></div></Form.FormItem>
            <Form.FormItem label="租户范围"><div className="token-editor-control"><Input aria-label={`策略 ${index + 1} 租户范围`} value={policy.tenant} disabled={saving} placeholder="留空表示全部租户" maxlength={200} onChange={value => updatePolicy(policy.id, { tenant: value })} /></div></Form.FormItem>
            <Form.FormItem label="项目范围"><div className="token-editor-control"><Input aria-label={`策略 ${index + 1} 项目范围`} value={policy.project} disabled={saving} placeholder="留空表示全部项目" maxlength={200} onChange={value => updatePolicy(policy.id, { project: value })} /></div></Form.FormItem>
          </div></Form>
          <div className="token-policy-toolbar"><Input aria-label="搜索 API 权限" value={search} clearable placeholder="搜索接口名称、模块或路径" onChange={setSearch} /><Space><Button variant="outline" disabled={saving} onClick={() => bulk('all')}>全选</Button><Button variant="outline" disabled={saving} onClick={() => bulk('read')}>选择只读</Button><Button variant="outline" disabled={saving} onClick={() => bulk('write')}>选择写入</Button><Button variant="text" disabled={saving} onClick={() => updatePolicy(policy.id, { permissions: [] })}>清空</Button></Space></div>
          {unavailable.length > 0 && <div className="token-policy-unavailable"><Alert theme="warning" message="以下已选接口已停用或不再可用，请移除后保存。" />{unavailable.map(permission => <div key={permission.endpoint_id}><span>{permission.summary || permission.path_template || permission.endpoint_id}</span><Button variant="text" theme="danger" disabled={saving} onClick={() => updatePolicy(policy.id, { permissions: policy.permissions.filter(item => item.endpoint_id !== permission.endpoint_id) })}>移除权限</Button></div>)}</div>}
          <Checkbox.Group className="token-policy-permissions" value={policy.permissions.map(permission => permission.endpoint_id)} disabled={saving} onChange={values => setSelected(values.map(String))}>{filtered.map(endpoint => <Checkbox key={endpoint.id} value={endpoint.id}><span className="token-policy-permission"><strong>{endpointName(endpoint)}</strong><span><Tag theme={action(endpoint.method) === 'read' ? 'primary' : 'warning'} variant="light">{endpoint.method}</Tag><code>{endpoint.path_template}</code><small>{endpoint.module || '未分类'} · {action(endpoint.method) === 'read' ? '读取' : '写入'}</small></span></span></Checkbox>)}</Checkbox.Group>
          {!filtered.length && <Empty text={available.length ? '没有匹配的 API 权限' : '服务尚无可授权接口，请前往开放 API 权限导入或新增'} />}
        </div>}
      </section>;
    })}</div>
  </>;
};
