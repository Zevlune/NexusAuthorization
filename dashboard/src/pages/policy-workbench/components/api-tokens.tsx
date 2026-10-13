import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Alert, Button, Card, Checkbox, Dialog, DialogPlugin, Form, Input, MessagePlugin, Pagination, Select, Space, Table, Tag, type TableProps } from 'tdesign-react';
import { AddIcon, CopyIcon, DeleteIcon, EditIcon, RefreshIcon, SearchIcon, ViewModuleIcon } from 'tdesign-icons-react';
import { getApiToken, listApiTokens, revokeApiToken, rotateApiToken, updateApiTokenStatus, type ApiToken, type ApiTokenPermission, type ApiTokenSecret, type ApiTokenStatus } from '../../../api/api-token';
import { getRequestErrorMessage } from '../../permission-center/shared';
import { Empty, fail, Guard, Page, type State } from '../shared';
import { useNavigate } from 'react-router-dom';
import './api-tokens.less';

const PAGE_SIZE = 25;
const STATUS_OPTIONS = [{ label: '全部状态', value: 'all' }, { label: '启用', value: 'active' }, { label: '停用', value: 'disabled' }, { label: '已撤销', value: 'revoked' }, { label: '已过期', value: 'expired' }];
const statusLabel: Record<ApiTokenStatus, string> = { active: '启用', disabled: '停用', revoked: '已撤销', expired: '已过期' };
const statusTheme: Record<ApiTokenStatus, 'success' | 'warning' | 'danger' | 'default'> = { active: 'success', disabled: 'warning', revoked: 'danger', expired: 'default' };
const isExpired = (token: ApiToken) => token.status !== 'revoked' && Boolean(token.expires_at && Date.parse(token.expires_at) <= Date.now());
const effectiveStatus = (token: ApiToken): ApiTokenStatus => token.status === 'revoked' || token.status === 'expired' || isExpired(token) ? (token.status === 'revoked' ? 'revoked' : 'expired') : token.status;
const canRotate = (token: ApiToken) => token.status === 'active' && !isExpired(token);

const errorText = (error: unknown, fallback: string) => {
  const message = getRequestErrorMessage(error, fallback);
  return message.toLowerCase().includes('403') || message.toLowerCase().includes('forbidden') ? '只有登录管理员可以管理 API Token' : message;
};
const dateLabel = (value: string | null, empty = '永久') => value ? new Date(value).toLocaleString() : empty;
const maskedToken = (prefix: string) => prefix ? `${prefix}${prefix.endsWith('...') ? '' : '••••••••'}` : '••••••••';

export const RawTokenDialog = ({ secret, onClose }: { secret: ApiTokenSecret | null; onClose: () => void }) => {
  const [copied, setCopied] = useState(false);
  useEffect(() => { setCopied(false); }, [secret]);
  if (!secret) return null;
  const copy = async () => {
    try { await navigator.clipboard.writeText(secret.raw_token); setCopied(true); MessagePlugin.success('Token 已复制'); }
    catch { MessagePlugin.error('复制失败，请手动复制'); }
  };
  const requestClose = () => {
    const dialog = DialogPlugin.confirm({ header: '确认关闭 Token 展示', body: '请确认你已经保存完整 Token。关闭后将无法再次查看。', confirmBtn: { content: '确认已保存', theme: 'primary' }, cancelBtn: '返回复制', onConfirm: () => { dialog.destroy(); onClose(); } });
  };
  return <Dialog visible header="新 Token 仅显示一次" width={560} confirmBtn={null} cancelBtn="关闭" onClose={requestClose} destroyOnClose>
    <Alert theme="warning" message="请立即复制并妥善保存。关闭后将无法再次查看完整 Token。" />
    <div className="api-token-secret"><code>{secret.raw_token}</code><Button theme="primary" icon={<CopyIcon />} onClick={() => void copy()}>{copied ? '已复制' : '复制 Token'}</Button></div>
  </Dialog>;
};

export default function ApiTokens({ state }: { state: State }) {
  const navigate = useNavigate();
  const [items, setItems] = useState<ApiToken[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [search, setSearch] = useState('');
  const [draftSearch, setDraftSearch] = useState('');
  const [status, setStatus] = useState<ApiTokenStatus | 'all'>('all');
  const [page, setPage] = useState(1);
  const [secret, setSecret] = useState<ApiTokenSecret | null>(null);
  const [detail, setDetail] = useState<ApiToken | null>(null);
  const requestID = useRef(0);
  const appKeyRef = useRef(state.appKey);
  appKeyRef.current = state.appKey;
  const load = useCallback(async () => {
    const appKey = state.appKey;
    if (!appKey) { setItems([]); setTotal(0); setLoading(false); return; }
    const id = ++requestID.current; setLoading(true); setLoadError('');
    try { const result = await listApiTokens(appKey, page, PAGE_SIZE, search, status); if (id !== requestID.current || appKey !== state.appKey) return; setItems(result.items); setTotal(result.total); }
    catch (err) { if (id === requestID.current) { setItems([]); setLoadError(errorText(err, '加载 API Token 失败')); } }
    finally { if (id === requestID.current) setLoading(false); }
  }, [page, search, state.appKey, status]);
  useEffect(() => { setPage(1); setSearch(''); setDraftSearch(''); setStatus('all'); setItems([]); setDetail(null); setSecret(null); }, [state.appKey]);
  useEffect(() => { void load(); }, [load]);
  const endpointsByID = useMemo(() => new Map(state.endpoints.map(endpoint => [endpoint.id, endpoint])), [state.endpoints]);
  const refresh = () => void load();
  const openDetail = async (token: ApiToken) => {
    const appKey = state.appKey;
    try { const latest = await getApiToken(appKey, token.id); if (appKeyRef.current === appKey) setDetail(latest); }
    catch (err) { if (appKeyRef.current === appKey) fail(err, errorText(err, '加载 Token 详情失败')); }
  };
  const mutateStatus = (token: ApiToken, action: 'toggle' | 'revoke' | 'rotate') => {
    const appKey = state.appKey;
    let revokeOld = true;
    const body: ReactNode = action === 'revoke'
      ? `确认撤销“${token.name}”吗？撤销后不可恢复。`
      : action === 'rotate'
        ? <Space direction="vertical" size="small"><span>轮换会生成新的完整 Token。</span><Checkbox defaultChecked onChange={value => { revokeOld = Boolean(value); }}>轮换后立即撤销旧 Token</Checkbox></Space>
        : `确认${token.status === 'active' ? '停用' : '启用'}“${token.name}”吗？`;
    const dialog = DialogPlugin.confirm({ header: action === 'rotate' ? '轮换 API Token' : action === 'revoke' ? '撤销 API Token' : '变更 Token 状态', body, confirmBtn: { content: '确认', theme: action === 'revoke' ? 'danger' : 'primary' }, cancelBtn: '取消', onConfirm: async () => {
      if (appKeyRef.current !== appKey) { dialog.destroy(); return; }
      try {
        if (action === 'toggle') await updateApiTokenStatus(appKey, token.id, token.status === 'active' ? 'disabled' : 'active');
        if (action === 'revoke') await revokeApiToken(appKey, token.id);
        if (action === 'rotate') {
          const nextSecret = await rotateApiToken(appKey, token.id, revokeOld);
          if (appKeyRef.current !== appKey) { dialog.destroy(); return; }
          setSecret(nextSecret);
        }
        if (appKeyRef.current !== appKey) { dialog.destroy(); return; }
        dialog.destroy(); await load();
      } catch (err) { dialog.destroy(); if (appKeyRef.current === appKey) fail(err, errorText(err, '操作 Token 失败')); }
    } });
  };
  const columns: TableProps<ApiToken>['columns'] = [
    { colKey: 'name', title: '名称', minWidth: 180, cell: ({ row }) => <Button variant="text" onClick={() => void openDetail(row)}>{row.name}</Button> },
    { colKey: 'owner', title: '类型', minWidth: 120, cell: ({ row }) => <span>{row.owner_type === 'user' ? '历史用户 Token' : '服务 Token'}</span> },
    { colKey: 'token_prefix', title: 'Token', minWidth: 150, cell: ({ row }) => <code>{maskedToken(row.token_prefix)}</code> },
    { colKey: 'status', title: '状态', width: 100, cell: ({ row }) => { const status = effectiveStatus(row); return <Tag theme={statusTheme[status]} variant="light-outline">{statusLabel[status]}</Tag>; } },
    { colKey: 'expires_at', title: '有效期至', width: 180, cell: ({ row }) => dateLabel(row.expires_at) },
    { colKey: 'permissions', title: '权限', width: 80, cell: ({ row }) => row.permissions.length },
    { colKey: 'actions', title: '操作', width: 390, fixed: 'right', cell: ({ row }) => { const status = effectiveStatus(row); return <Space size="small"><Button variant="text" icon={<ViewModuleIcon />} onClick={() => void openDetail(row)}>详情</Button><Button variant="text" icon={<EditIcon />} onClick={() => navigate(`/api-tokens/${encodeURIComponent(row.id)}/edit`)} disabled={row.status === 'revoked'}>编辑</Button><Button variant="text" onClick={() => navigate(`/api-tokens/${encodeURIComponent(row.id)}/permissions`)} disabled={row.status === 'revoked'}>权限策略</Button>{status === 'active' ? <Button variant="text" onClick={() => mutateStatus(row, 'toggle')}>停用</Button> : status === 'disabled' ? <Button variant="text" onClick={() => mutateStatus(row, 'toggle')}>启用</Button> : null}<Button variant="text" onClick={() => mutateStatus(row, 'rotate')} disabled={!canRotate(row)}>轮换</Button><Button variant="text" theme="danger" icon={<DeleteIcon />} onClick={() => mutateStatus(row, 'revoke')} disabled={row.status === 'revoked'}>撤销</Button></Space>; } },
  ];
  const detailPermission = (permission: ApiTokenPermission) => { const endpoint = endpointsByID.get(permission.endpoint_id); return `${permission.summary || endpoint?.summary || endpoint?.endpoint_key || '未命名接口'} · ${permission.method || endpoint?.method || ''} ${permission.path_template || endpoint?.path_template || ''} · ${permission.action} · ${permission.tenant_id || '租户不限'} / ${permission.project_id || '项目不限'}`; };
  return <Guard state={state}><Page title="API Token 管理" actions={<Space><Button variant="outline" onClick={() => navigate('/api-permission-groups')}>维护 API 权限组</Button><Button variant="outline" onClick={() => navigate('/open-api-permissions')}>维护开放 API 权限</Button><Button icon={<RefreshIcon />} variant="outline" onClick={refresh}>刷新</Button><Button theme="primary" icon={<AddIcon />} onClick={() => navigate('/api-tokens/create')}>创建 Token</Button></Space>}>
    {loadError && <Alert className="api-token-error" theme="error" message={loadError} operation={<Button variant="text" onClick={refresh}>重试</Button>} />}
    <div className="page-filter-bar"><Form className="workbench-filter-form" layout="inline" colon={false} onSubmit={() => { setPage(1); setSearch(draftSearch); }}><Form.FormItem label="关键词"><div className="api-token-control"><Input className="workbench-keyword" value={draftSearch} clearable placeholder="搜索 Token 名称或主体" prefixIcon={<SearchIcon />} onChange={setDraftSearch} /></div></Form.FormItem><Form.FormItem label="状态"><div className="api-token-control"><Select className="workbench-select" value={status} options={STATUS_OPTIONS} onChange={value => { setPage(1); setStatus(value as ApiTokenStatus | 'all'); }} /></div></Form.FormItem><Button type="submit" theme="primary" icon={<SearchIcon />}>查询</Button></Form></div>
    <Card className="rbac-card rbac-table-card"><Table rowKey="id" data={items} columns={columns} loading={loading} hover empty={<Empty text="尚无 API Token" />} /></Card><Pagination className="api-token-pagination" current={page} pageSize={PAGE_SIZE} total={total} onCurrentChange={setPage} />
    <RawTokenDialog secret={secret} onClose={() => setSecret(null)} />
    <Dialog visible={Boolean(detail)} header="API Token 详情" width={680} confirmBtn={null} cancelBtn="关闭" onCancel={() => setDetail(null)} onClose={() => setDetail(null)} destroyOnClose>{detail && <DescriptionsLike token={detail} permissionLabel={detailPermission} />}</Dialog>
  </Page></Guard>;
}

const DescriptionsLike = ({ token, permissionLabel }: { token: ApiToken; permissionLabel: (permission: ApiTokenPermission) => string }) => { const status = effectiveStatus(token); return <div className="api-token-detail"><p><strong>名称</strong>{token.name}</p><p><strong>主体</strong>{token.owner_type === 'user' ? '用户' : '服务'} · {token.owner_id}</p><p><strong>Token</strong><code>{maskedToken(token.token_prefix)}</code></p><p><strong>状态</strong><Tag theme={statusTheme[status]}>{statusLabel[status]}</Tag></p><p><strong>有效期</strong>{dateLabel(token.expires_at)}</p><p><strong>最近使用</strong>{dateLabel(token.last_used_at, '未使用')}</p><strong>权限</strong><ul>{token.permissions.map(permission => <li key={`${permission.endpoint_id}-${permission.action}-${permission.tenant_id}-${permission.project_id}`}>{permissionLabel(permission)}</li>)}</ul></div>; };
