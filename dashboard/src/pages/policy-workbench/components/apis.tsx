import { useMemo, useState, type Dispatch, type ReactNode, type SetStateAction } from 'react';
import {
  Button,
  Card,
  Alert,
  Descriptions,
  DialogPlugin,
  Drawer,
  Form,
  Input,
  MessagePlugin,
  Select,
  Space,
  Switch,
  Table,
  Pagination,
  Tag,
  Textarea,
} from 'tdesign-react';
import type { TableProps } from 'tdesign-react/es/table/interface';
import { AddIcon, DeleteIcon, EditIcon, UploadIcon, SearchIcon, RefreshIcon } from 'tdesign-icons-react';
import { useLocation } from 'react-router-dom';
import { deleteEndpoint, importSwagger, saveEndpoint, type Endpoint } from '../../../api/rbac-v1';
import { Empty, fail, Guard, Page, type State } from '../shared';
import './apis.less';

type EndpointDraft = {
  endpoint_key: string;
  service_resource: string;
  method: string;
  path_template: string;
  module: string;
  summary: string;
  enabled: boolean;
};

type FormErrors = Partial<Record<keyof EndpointDraft, string>>;
type ImportResult = { total: number; created: number; skipped: number };
type EndpointTableRow = Endpoint & Record<string, unknown>;

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'TRACE'];
const PAGE_SIZE = 25;

const methodTheme = (method: string): 'primary' | 'success' | 'warning' | 'danger' | 'default' => (
  { GET: 'primary', POST: 'success', PUT: 'warning', PATCH: 'warning', DELETE: 'danger' }[method] || 'default'
) as 'primary' | 'success' | 'warning' | 'danger' | 'default';

const serviceLabel = (service: State['services'][number]) => service.displayName || service.name || service.key;

const emptyDraft = (service = ''): EndpointDraft => ({
  endpoint_key: '',
  service_resource: service,
  method: 'GET',
  path_template: '',
  module: '',
  summary: '',
  enabled: true,
});

const updateDraft = (setDraft: Dispatch<SetStateAction<(EndpointDraft & { id?: string }) | null>>, patch: Partial<EndpointDraft>) => {
  setDraft((current) => current ? { ...current, ...patch } : current);
};

const Footer = ({ cancel, submit, loading, submitText }: { cancel: () => void; submit: () => void; loading: boolean; submitText: string }) => (
  <Space className="resource-apis-drawer-footer">
    <Button variant="text" disabled={loading} onClick={cancel}>取消</Button>
    <Button theme="primary" loading={loading} disabled={loading} onClick={submit}>{submitText}</Button>
  </Space>
);

const Field = ({ label, required, error, children }: { label: string; required?: boolean; error?: string; children: ReactNode }) => (
  <Form.FormItem label={required ? `${label} *` : label} help={error} status={error ? 'error' : undefined}>
    {children}
  </Form.FormItem>
);

export default function APIs({ state }: { state: State }) {
  const activeServices = useMemo(() => state.services.filter((service) => service.isActive), [state.services]);
  const [search, setSearch] = useState('');
  const [draftSearch, setDraftSearch] = useState('');
  const [draftService, setDraftService] = useState<string | null>(null);
  const [draftMethod, setDraftMethod] = useState('');
  const [serviceFilter, setServiceFilter] = useState('');
  const [serviceFilterSearch, setServiceFilterSearch] = useState<string | null>(null);
  const [methodFilter, setMethodFilter] = useState('');
  const [page, setPage] = useState(1);
  const [edit, setEdit] = useState<EndpointDraft & { id?: string } | null>(null);
  const [errors, setErrors] = useState<FormErrors>({});
  const [editError, setEditError] = useState('');
  const [saving, setSaving] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [importService, setImportService] = useState('');
  const [swaggerURL, setSwaggerURL] = useState('');
  const [importError, setImportError] = useState('');
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const location = useLocation();

  const queryService = useMemo(() => new URLSearchParams(location.search).get('service')?.trim() || '', [location.search]);
  const effectiveServiceFilter = serviceFilterSearch === location.search
    ? serviceFilter
    : (activeServices.some((service) => service.key === queryService) ? queryService : '');
  const filteredEndpoints = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    return state.endpoints.filter((endpoint) => {
      const matchesKeyword = !keyword || [endpoint.endpoint_key, endpoint.summary, endpoint.path_template, endpoint.module, endpoint.service_resource]
        .join(' ').toLowerCase().includes(keyword);
      return matchesKeyword
        && (!effectiveServiceFilter || endpoint.service_resource === effectiveServiceFilter)
        && (!methodFilter || endpoint.method === methodFilter);
    });
  }, [effectiveServiceFilter, methodFilter, search, state.endpoints]);
  const pageCount = Math.max(1, Math.ceil(filteredEndpoints.length / PAGE_SIZE));
  const displayPage = Math.min(page, pageCount);
  const visibleEndpoints = filteredEndpoints.slice((displayPage - 1) * PAGE_SIZE, displayPage * PAGE_SIZE) as EndpointTableRow[];

  const endpointColumns: TableProps<EndpointTableRow>['columns'] = [
    {
      colKey: 'endpoint',
      title: '摘要 / 路径',
      minWidth: 260,
      cell: ({ row }) => <div className="resource-apis-endpoint-cell"><strong>{row.summary || row.endpoint_key || '-'}</strong><code>{row.path_template || '-'}</code></div>,
    },
    { colKey: 'service_resource', title: '服务', cell: ({ row }) => <Tag variant="light-outline">{row.service_resource}</Tag> },
    { colKey: 'method', title: '方法', cell: ({ row }) => <Tag theme={methodTheme(row.method)} variant="light-outline">{row.method}</Tag> },
    { colKey: 'module', title: '模块', cell: ({ row }) => row.module || '-' },
    { colKey: 'enabled', title: '状态', cell: ({ row }) => <Tag theme={row.enabled ? 'success' : 'default'} variant="light-outline">{row.enabled ? '启用' : '停用'}</Tag> },
    {
      colKey: 'operation',
      title: '操作',
      width: 170,
      cell: ({ row }) => <Space size="small"><Button variant="text" icon={<EditIcon />} onClick={() => openEdit(row)}>编辑</Button><Button variant="text" theme="danger" icon={<DeleteIcon />} onClick={() => confirmDelete(row)}>删除</Button></Space>,
    },
  ];

  const clearFilters = () => {
    setDraftSearch('');
    setDraftService('');
    setDraftMethod('');
    setSearch('');
    setServiceFilter('');
    setServiceFilterSearch(location.search);
    setMethodFilter('');
    setPage(1);
  };

  const openCreate = () => {
    setErrors({});
    setEditError('');
    setEdit(emptyDraft(activeServices[0]?.key));
  };

  const openEdit = (endpoint: Endpoint) => {
    setErrors({});
    setEditError('');
    setEdit({
      id: endpoint.id,
      endpoint_key: endpoint.endpoint_key || '',
      service_resource: endpoint.service_resource,
      method: endpoint.method || 'GET',
      path_template: endpoint.path_template || '',
      module: endpoint.module || '',
      summary: endpoint.summary || '',
      enabled: endpoint.enabled !== false,
    });
  };

  const closeEdit = () => {
    if (!saving) {
      setEdit(null);
      setErrors({});
    }
  };

  const validateDraft = (draft: EndpointDraft): FormErrors => {
    const next: FormErrors = {};
    if (!draft.endpoint_key.trim()) next.endpoint_key = '请输入接口标识';
    if (!draft.service_resource) next.service_resource = '请选择目标服务';
    if (!draft.method) next.method = '请选择 HTTP 方法';
    if (!draft.path_template.trim()) next.path_template = '请输入路径';
    else if (!draft.path_template.trim().startsWith('/')) next.path_template = '路径必须以 / 开头';
    return next;
  };

  const submitEdit = async () => {
    if (saving || !edit) return;
    const nextErrors = validateDraft(edit);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length) return;
    setEditError('');
    setSaving(true);
    try {
      await saveEndpoint(state.appKey, edit.service_resource, {
        ...(edit.id ? { id: edit.id } : {}),
        endpoint_key: edit.endpoint_key.trim(),
        method: edit.method,
        path_template: edit.path_template.trim(),
        module: edit.module.trim(),
        summary: edit.summary.trim(),
        enabled: edit.enabled,
      });
      setEdit(null);
      await state.reload();
      MessagePlugin.success(edit.id ? 'API 已更新' : 'API 已创建');
    } catch (error) {
      setEditError(error instanceof Error ? error.message : (edit.id ? '更新 API 失败' : '创建 API 失败'));
      fail(error, edit.id ? '更新 API 失败' : '创建 API 失败');
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = (endpoint: Endpoint) => {
    const dialog = DialogPlugin.confirm({
      header: '删除 API 接口',
      body: `确认删除“${endpoint.summary || endpoint.path_template}”吗？删除后将无法参与权限配置。`,
      theme: 'warning',
      confirmBtn: { content: '确认删除', theme: 'danger' },
      cancelBtn: '取消',
      onConfirm: async () => {
        try {
          await deleteEndpoint(state.appKey, endpoint.service_resource, endpoint.id);
          dialog.destroy();
          await state.reload();
          MessagePlugin.success('API 已删除');
        } catch (error) {
          fail(error, '删除 API 失败');
        }
      },
    });
  };

  const openImport = () => {
    setImportService('');
    setSwaggerURL('');
    setImportError('');
    setImportResult(null);
    setImportOpen(true);
  };

  const closeImport = () => {
    if (!importing) setImportOpen(false);
  };

  const submitImport = async () => {
    if (importing || importResult) return;
    const url = swaggerURL.trim();
    setImportError('');
    if (!importService) {
      setImportError('请选择目标服务');
      return;
    }
    if (!/^https?:\/\/[^\s]+$/i.test(url)) {
      setImportError('请输入有效的 http(s) 地址');
      return;
    }
    setImporting(true);
    try {
      const result = await importSwagger(state.appKey, importService, url);
      setImportResult(result);
      await state.reload();
    } catch (error) {
      setImportError(error instanceof Error ? error.message : '导入 Swagger 失败');
    } finally {
      setImporting(false);
    }
  };

  const resetImport = () => {
    if (importing) return;
    setImportResult(null);
    setSwaggerURL('');
    setImportError('');
  };

  const setFilterService = (value: unknown) => {
    setServiceFilter(String(value || ''));
    setServiceFilterSearch(location.search);
    setPage(1);
  };

  return (
    <Guard state={state}>
      <div className="resource-apis">
        <Page
          title="API 接口"
          actions={(
            <Space>
              <Button variant="outline" icon={<UploadIcon />} disabled={!activeServices.length} onClick={openImport}>导入 Swagger</Button>
              <Button theme="primary" icon={<AddIcon />} disabled={!activeServices.length} onClick={openCreate}>新增 API</Button>
            </Space>
          )}
        >
          {state.loading ? (
            <Card className="resource-apis-card"><Empty text="正在加载 API 资源..." /></Card>
          ) : activeServices.length === 0 ? (
            <Card className="resource-apis-card">
              <Empty text="当前应用尚未关联可用服务" />
              <div className="resource-apis-empty-action"><Button theme="primary" onClick={() => { window.location.href = '/service-resources'; }}>前往关联服务</Button></div>
            </Card>
          ) : (
            <>
              <div className="page-filter-bar">
                <Form className="workbench-filter-form" layout="inline" colon={false} labelWidth={100} onSubmit={() => { setSearch(draftSearch); setFilterService(draftService ?? effectiveServiceFilter); setMethodFilter(draftMethod); setPage(1); }}>
                  <Form.FormItem label="关键词"><Input className="workbench-keyword" value={draftSearch} placeholder="摘要、路径、模块或服务" prefixIcon={<SearchIcon />} clearable onChange={setDraftSearch} /></Form.FormItem>
                  <Form.FormItem label="服务"><Select className="workbench-select" value={draftService ?? effectiveServiceFilter} placeholder="全部服务" clearable options={activeServices.map((service) => ({ label: `${serviceLabel(service)} · ${service.key}`, value: service.key }))} onChange={value => setDraftService(String(value || ''))} /></Form.FormItem>
                  <Form.FormItem label="方法"><Select className="workbench-select" value={draftMethod} placeholder="全部方法" clearable options={METHODS.map((method) => ({ label: method, value: method }))} onChange={value => setDraftMethod(String(value || ''))} /></Form.FormItem>
                  <Form.FormItem><Space size={16}><Button theme="primary" type="submit" icon={<SearchIcon />}>查询</Button><Button theme="primary" icon={<RefreshIcon />} onClick={clearFilters}>重置</Button></Space></Form.FormItem>
                </Form>
              </div>
              <Card className="resource-apis-card">
              <div className="resource-apis-table-wrap">
                <Table<EndpointTableRow>
                  className="resource-apis-table"
                  rowKey="id"
                  columns={endpointColumns}
                  data={visibleEndpoints}
                  empty={<Empty text={state.endpoints.length ? '没有匹配的 API 接口' : '暂无 API 接口'} />}
                  hover
                  size="small"
                />
              </div>
              <div className="resource-apis-pagination">
                <Pagination current={displayPage} total={filteredEndpoints.length} pageSize={PAGE_SIZE} showPageSize={false} onCurrentChange={setPage} />
              </div>
              </Card>
            </>
          )}
        </Page>

        <Drawer
          className="resource-apis resource-apis-drawer"
          visible={Boolean(edit)}
          header={edit?.id ? '编辑 API 接口' : '新增 API 接口'}
          size="560px"
          footer={edit ? <Footer cancel={closeEdit} submit={() => void submitEdit()} loading={saving} submitText="保存" /> : null}
          onClose={closeEdit}
          onCancel={closeEdit}
          destroyOnClose
        >
          {edit ? <Form className="resource-apis-form" labelAlign="top">
            {editError ? <Alert className="resource-apis-form-error" theme="error" message={editError} /> : null}
            <Field label="接口标识" required error={errors.endpoint_key}><Input disabled={saving} value={edit.endpoint_key} placeholder="例如 orders.list" maxlength={200} onChange={(value) => updateDraft(setEdit, { endpoint_key: value })} /></Field>
            <Field label="摘要"><Textarea disabled={saving} value={edit.summary} placeholder="用于列表展示和权限说明" maxlength={300} onChange={(value) => updateDraft(setEdit, { summary: value })} /></Field>
            <Field label="目标服务" required error={errors.service_resource}><Select disabled={saving || Boolean(edit.id)} value={edit.service_resource} placeholder="请选择服务" options={activeServices.map((service) => ({ label: `${serviceLabel(service)} · ${service.key}`, value: service.key }))} onChange={(value) => updateDraft(setEdit, { service_resource: String(value || '') })} /></Field>
            <div className="resource-apis-form-grid">
              <Field label="HTTP 方法" required error={errors.method}><Select disabled={saving} value={edit.method} options={METHODS.map((method) => ({ label: method, value: method }))} onChange={(value) => updateDraft(setEdit, { method: String(value) })} /></Field>
              <Field label="模块"><Input disabled={saving} value={edit.module} placeholder="例如用户中心" onChange={(value) => updateDraft(setEdit, { module: value })} /></Field>
            </div>
            <Field label="路径" required error={errors.path_template}><Input disabled={saving} value={edit.path_template} placeholder="例如 /v1/users/{id}" onChange={(value) => updateDraft(setEdit, { path_template: value })} /></Field>
            <Form.FormItem label="启用接口" help="停用接口无法通过权限授权"><Switch aria-label="启用接口" disabled={saving} value={edit.enabled} onChange={(value) => updateDraft(setEdit, { enabled: Boolean(value) })} /></Form.FormItem>
          </Form> : null}
        </Drawer>

        <Drawer
          className="resource-apis resource-apis-drawer"
          visible={importOpen}
          header="导入 Swagger"
          size="560px"
          footer={<Footer cancel={closeImport} submit={() => void (importResult ? resetImport() : submitImport())} loading={importing} submitText={importResult ? '再导入' : '开始导入'} />}
          onClose={closeImport}
          onCancel={closeImport}
          destroyOnClose
        >
          <Form className="resource-apis-form" labelAlign="top">
            <Field label="目标服务" required error={importError && !importService ? importError : undefined}><Select disabled={importing || Boolean(importResult)} value={importService} placeholder="请选择已关联且启用的服务" options={activeServices.map((service) => ({ label: `${serviceLabel(service)} · ${service.key}`, value: service.key }))} onChange={(value) => { setImportService(String(value || '')); setImportError(''); }} /></Field>
            <Field label="Swagger URL" required error={importError && importService ? importError : undefined}><Input disabled={importing || Boolean(importResult)} value={swaggerURL} placeholder="https://example.com/openapi.json" onChange={(value) => { setSwaggerURL(value); setImportError(''); }} /></Field>
            <p className="resource-apis-import-note">导入到共享服务目录，不会自动授予应用或角色权限。</p>
            {importResult ? <Descriptions
              className="resource-apis-import-result"
              title="导入完成"
              bordered
              column={3}
              items={[
                { label: '总计', content: `${importResult.total} 个接口` },
                { label: '新增', content: `${importResult.created} 个` },
                { label: '跳过', content: `${importResult.skipped} 个` },
              ]}
            /> : null}
          </Form>
        </Drawer>
      </div>
    </Guard>
  );
}
