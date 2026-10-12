import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Card, Dialog, Empty, Form, Input, Loading, MessagePlugin, Select, Space, Table, Tag, type TableProps } from 'tdesign-react';

import {
  createAPIEndpoint,
  deleteAPIEndpoint,
  importSwaggerAPIEndpoints,
  listAPIEndpoints,
  type APIEndpoint,
  type CreateAPIEndpointRequest,
  type ImportSwaggerAPIEndpointsResult,
  type UpdateAPIEndpointRequest,
  updateAPIEndpoint,
} from '../../../api/permission';
import { formatDateTime, getRequestErrorMessage, PageHeader, useServiceResourceScope } from '../shared';
import '../style.less';

type EndpointForm = {
  controller: string;
  method: string;
  pathTemplate: string;
  summary: string;
  enabled: boolean;
};

const EMPTY_FORM: EndpointForm = {
  controller: '',
  method: 'GET',
  pathTemplate: '',
  summary: '',
  enabled: true,
};

const methodOptions = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'TRACE']
  .map((method) => ({ label: method, value: method }));

const statusOptions = [
  { label: '启用', value: 'enabled' },
  { label: '停用', value: 'disabled' },
];

const notify = (type: 'success' | 'error' | 'warning', content: string) => {
  try {
    MessagePlugin[type](content);
  } catch {
    // Keep the completed request from being interrupted when no toast host exists.
  }
};

const endpointPath = (endpoint: APIEndpoint) => `${endpoint.method || 'ANY'} ${endpoint.pathTemplate || '-'}`;

const APIEndpointManagementPage = () => {
  const { serviceResource } = useServiceResourceScope();
  const [endpoints, setEndpoints] = useState<APIEndpoint[]>([]);
  const [loading, setLoading] = useState(false);
  const [dialogVisible, setDialogVisible] = useState(false);
  const [form, setForm] = useState<EndpointForm>({ ...EMPTY_FORM });
  const [editingEndpoint, setEditingEndpoint] = useState<APIEndpoint | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<APIEndpoint | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [swaggerDialogVisible, setSwaggerDialogVisible] = useState(false);
  const [swaggerURL, setSwaggerURL] = useState('');
  const [importingSwagger, setImportingSwagger] = useState(false);
  const [lastImport, setLastImport] = useState<ImportSwaggerAPIEndpointsResult | null>(null);

  const loadEndpoints = useCallback(async (scope: string) => {
    setLoading(true);
    try {
      setEndpoints(await listAPIEndpoints(scope));
    } catch (error) {
      setEndpoints([]);
      notify('error', getRequestErrorMessage(error, '加载 API 端点失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadEndpoints(serviceResource);
  }, [loadEndpoints, serviceResource]);

  const resetForm = () => {
    setEditingEndpoint(null);
    setForm({ ...EMPTY_FORM });
  };

  const openCreateDialog = () => {
    resetForm();
    setDialogVisible(true);
  };

  const openEditDialog = (endpoint: APIEndpoint) => {
    setEditingEndpoint(endpoint);
    setForm({
      controller: endpoint.controller,
      method: endpoint.method || 'GET',
      pathTemplate: endpoint.pathTemplate,
      summary: endpoint.summary,
      enabled: endpoint.enabled,
    });
    setDialogVisible(true);
  };

  const closeDialog = () => {
    if (saving) return;
    setDialogVisible(false);
    resetForm();
  };

  const submitForm = async () => {
    const controller = form.controller.trim();
    const pathTemplate = form.pathTemplate.trim();
    if (!controller || !pathTemplate) {
      notify('warning', '请填写控制器和路径模板');
      return;
    }

    const editing = editingEndpoint;
    setSaving(true);
    try {
      if (editing) {
        const payload: UpdateAPIEndpointRequest = {
          controller,
          method: form.method,
          path_template: pathTemplate,
          summary: form.summary.trim(),
          enabled: form.enabled,
        };
        const updated = await updateAPIEndpoint(editing.id, payload);
        setEndpoints((current) => current.map((item) => item.id === updated.id ? updated : item));
      } else {
        const payload: CreateAPIEndpointRequest = {
          service_resource: serviceResource,
          controller,
          method: form.method,
          path_template: pathTemplate,
          summary: form.summary.trim(),
          enabled: form.enabled,
        };
        const created = await createAPIEndpoint(payload);
        setEndpoints((current) => [...current, created]);
      }
      setDialogVisible(false);
      resetForm();
      notify('success', editing ? 'API 端点已更新' : 'API 端点已创建');
    } catch (error) {
      notify('error', getRequestErrorMessage(error, editing ? '更新 API 端点失败' : '创建 API 端点失败'));
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleting(true);
    try {
      await deleteAPIEndpoint(target.id);
      setEndpoints((current) => current.filter((item) => item.id !== target.id));
      setDeleteTarget(null);
      notify('success', 'API 端点已删除');
    } catch (error) {
      notify('error', getRequestErrorMessage(error, '删除 API 端点失败'));
    } finally {
      setDeleting(false);
    }
  };

  const openSwaggerDialog = () => {
    setSwaggerURL('');
    setLastImport(null);
    setSwaggerDialogVisible(true);
  };

  const closeSwaggerDialog = () => {
    if (importingSwagger) return;
    setSwaggerDialogVisible(false);
    setSwaggerURL('');
    setLastImport(null);
  };

  const submitSwaggerImport = async () => {
    const url = swaggerURL.trim();
    if (!url) {
      notify('warning', '请输入 Swagger URL');
      return;
    }
    setImportingSwagger(true);
    try {
      const result = await importSwaggerAPIEndpoints({
        service_resource: serviceResource,
        swagger_url: url,
      });
      setLastImport(result);
      setSwaggerDialogVisible(false);
      setSwaggerURL('');
      await loadEndpoints(serviceResource);
      notify('success', `Swagger 导入完成：新增 ${result.created} 个，跳过 ${result.skipped} 个`);
    } catch (error) {
      notify('error', getRequestErrorMessage(error, '导入 Swagger 失败'));
    } finally {
      setImportingSwagger(false);
    }
  };

  const columns: TableProps<APIEndpoint>['columns'] = [
    { colKey: 'controller', title: '控制器', minWidth: 180, className: 'permission-table-name', cell: ({ row }) => row.controller || '-' },
    { colKey: 'method', title: 'Method', width: 110, cell: ({ row }) => <Tag theme="primary" variant="light-outline">{row.method || 'ANY'}</Tag> },
    { colKey: 'pathTemplate', title: 'Path', minWidth: 230, className: 'permission-table-code', cell: ({ row }) => row.pathTemplate || '-' },
    { colKey: 'summary', title: 'Summary', minWidth: 180, className: 'permission-table-muted', cell: ({ row }) => row.summary || '-' },
    { colKey: 'enabled', title: '状态', width: 90, cell: ({ row }) => <Tag theme={row.enabled ? 'success' : 'default'} variant="light-outline">{row.enabled ? '启用' : '停用'}</Tag> },
    { colKey: 'updatedAt', title: '更新时间', minWidth: 170, className: 'permission-table-muted', cell: ({ row }) => formatDateTime(row.updatedAt || row.createdAt) },
    {
      colKey: 'actions',
      title: '操作',
      minWidth: 150,
      cell: ({ row }) => (
        <Space size="small">
          <Button variant="text" theme="primary" type="button" onClick={() => openEditDialog(row)}>编辑</Button>
          <Button variant="text" theme="danger" type="button" onClick={() => setDeleteTarget(row)}>删除</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="permission-page permission-api-endpoint-page">
      <PageHeader
        title="API 端点管理"
        actions={(
          <Space>
            <Button variant="outline" theme="primary" type="button" onClick={openSwaggerDialog}>导入 Swagger</Button>
            <Button theme="primary" type="button" onClick={openCreateDialog}>新建端点</Button>
          </Space>
        )}
      />

      <div className="permission-toolbar">
        <span className="permission-toolbar-meta">当前服务资源：<code>{serviceResource}</code></span>
        <span className="permission-toolbar-meta">共 {endpoints.length} 个 API 端点</span>
      </div>

      {lastImport ? (
        <Alert
          className="permission-api-endpoint-import-result"
          theme="success"
          message={`最近一次导入：总计 ${lastImport.total} 个，新增 ${lastImport.created} 个，跳过 ${lastImport.skipped} 个`}
        />
      ) : null}

      <Card className="permission-card" bordered>
        <div className="permission-table-wrap">
          <Table<APIEndpoint>
            className="permission-table permission-api-endpoint-table"
            rowKey="id"
            data={loading ? [] : endpoints}
            columns={columns}
            loading={loading ? <Loading text="正在加载 API 端点..." /> : false}
            hover
            tableLayout="auto"
            empty={<Empty description="当前服务资源暂无 API 端点" />}
          />
        </div>
      </Card>

      <Dialog
        visible={dialogVisible}
        header={editingEndpoint ? '编辑 API 端点' : '新建 API 端点'}
        width={620}
        confirmBtn={{ content: editingEndpoint ? '保存' : '创建', theme: 'primary', loading: saving }}
        cancelBtn="取消"
        confirmLoading={saving}
        onConfirm={() => void submitForm()}
        onCancel={closeDialog}
        onClose={closeDialog}
        destroyOnClose
      >
        <Form className="permission-inline-form" labelAlign="top">
          <Form.FormItem label="服务资源" className="full-width">
            <div className="permission-readonly-field"><code>{serviceResource}</code><span>当前已选服务资源</span></div>
          </Form.FormItem>
          <Form.FormItem label="控制器">
            <Input
              value={form.controller}
              maxlength={200}
              placeholder="例如 UserController"
              onChange={(value) => setForm((prev) => ({ ...prev, controller: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="Method">
            <Select value={form.method} options={methodOptions} onChange={(value) => setForm((prev) => ({ ...prev, method: String(value) }))} />
          </Form.FormItem>
          <Form.FormItem label="Path 模板" className="full-width">
            <Input
              value={form.pathTemplate}
              maxlength={500}
              placeholder="例如 /v1/users/{id}"
              onChange={(value) => setForm((prev) => ({ ...prev, pathTemplate: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="Summary" className="full-width">
            <Input
              value={form.summary}
              maxlength={300}
              placeholder="例如 查询用户详情"
              onChange={(value) => setForm((prev) => ({ ...prev, summary: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="状态">
            <Select value={form.enabled ? 'enabled' : 'disabled'} options={statusOptions} onChange={(value) => setForm((prev) => ({ ...prev, enabled: value === 'enabled' }))} />
          </Form.FormItem>
        </Form>
      </Dialog>

      <Dialog
        visible={Boolean(deleteTarget)}
        header="删除 API 端点"
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
          确认删除 <strong>{deleteTarget ? endpointPath(deleteTarget) : ''}</strong>？删除后该端点将不再参与当前服务资源的权限校验。
        </p>
      </Dialog>

      <Dialog
        visible={swaggerDialogVisible}
        header="导入 Swagger API 端点"
        width={600}
        confirmBtn={{ content: '开始导入', theme: 'primary', loading: importingSwagger }}
        cancelBtn="取消"
        confirmLoading={importingSwagger}
        onConfirm={() => void submitSwaggerImport()}
        onCancel={closeSwaggerDialog}
        onClose={closeSwaggerDialog}
        destroyOnClose
      >
        <Form className="permission-import-form" labelAlign="top">
          <Form.FormItem label="服务资源">
            <div className="permission-readonly-field"><code>{serviceResource}</code><span>导入将写入当前已选服务资源</span></div>
          </Form.FormItem>
          <Form.FormItem label="Swagger URL" help="支持 Swagger UI、Swagger 2.0 和 OpenAPI 3.x 文档地址。">
            <Input
              value={swaggerURL}
              placeholder="例如 https://example.com/swagger/openapi.json"
              onChange={setSwaggerURL}
            />
          </Form.FormItem>
        </Form>
      </Dialog>
    </div>
  );
};

export default APIEndpointManagementPage;
