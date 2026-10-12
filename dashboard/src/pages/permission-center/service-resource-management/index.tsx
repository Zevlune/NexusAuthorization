import { useState } from 'react';
import { Button, Card, Dialog, Empty, Form, Input, Loading, MessagePlugin, Select, Space, Table, Tag, type TableProps } from 'tdesign-react';

import {
  createServiceResource,
  deleteServiceResource,
  type ServiceResource,
  type CreateServiceResourceRequest,
  type UpdateServiceResourceRequest,
  updateServiceResource,
} from '../../../api/permission';
import {
  formatDateTime,
  getRequestErrorMessage,
  PageHeader,
  useServiceResources,
} from '../shared';
import '../style.less';

type ServiceResourceForm = {
  key: string;
  displayName: string;
  audience: string;
  description: string;
  isActive: boolean;
};

const EMPTY_FORM: ServiceResourceForm = {
  key: '',
  displayName: '',
  audience: '',
  description: '',
  isActive: true,
};

const statusOptions = [
  { label: '启用', value: 'enabled' },
  { label: '停用', value: 'disabled' },
];

const sourceLabel = (source: ServiceResource['source']): string => source === 'local' ? '本地' : 'NexusAuth';

const sourceTheme = (source: ServiceResource['source']): 'primary' | 'default' => source === 'local' ? 'primary' : 'default';

const ServiceResourceManagementPage = () => {
  const {
    serviceResources,
    serviceResourcesWritable,
    serviceResourcesLoading,
    serviceResourcesError,
    reloadServiceResources,
  } = useServiceResources();
  const [dialogVisible, setDialogVisible] = useState(false);
  const [form, setForm] = useState<ServiceResourceForm>({ ...EMPTY_FORM });
  const [editingResource, setEditingResource] = useState<ServiceResource | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ServiceResource | null>(null);
  const [deleting, setDeleting] = useState(false);

  const resetForm = () => {
    setEditingResource(null);
    setForm({ ...EMPTY_FORM });
  };

  const openCreateDialog = () => {
    resetForm();
    setDialogVisible(true);
  };

  const openEditDialog = (resource: ServiceResource) => {
    if (resource.source !== 'local') return;
    setEditingResource(resource);
    setForm({
      key: resource.key,
      displayName: resource.displayName,
      audience: resource.audience,
      description: resource.description,
      isActive: resource.isActive,
    });
    setDialogVisible(true);
  };

  const closeDialog = () => {
    if (saving) return;
    setDialogVisible(false);
    resetForm();
  };

  const submitForm = async () => {
    const key = form.key.trim();
    const displayName = form.displayName.trim();
    if (!key || !displayName) {
      MessagePlugin.warning('请填写唯一 Key 和显示名称');
      return;
    }

    const editing = editingResource;
    setSaving(true);
    try {
      if (editing) {
        const payload: UpdateServiceResourceRequest = {
          display_name: displayName,
          audience: form.audience.trim(),
          description: form.description.trim(),
          is_active: form.isActive,
        };
        await updateServiceResource(editing.key, payload);
      } else {
        const payload: CreateServiceResourceRequest = {
          key,
          display_name: displayName,
          audience: form.audience.trim(),
          description: form.description.trim(),
          is_active: form.isActive,
        };
        await createServiceResource(payload);
      }
      setDialogVisible(false);
      resetForm();
      await reloadServiceResources();
      MessagePlugin.success(editing ? '服务资源已更新' : '服务资源已创建');
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, editing ? '更新服务资源失败' : '创建服务资源失败'));
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleting(true);
    try {
      await deleteServiceResource(target.key);
      setDeleteTarget(null);
      await reloadServiceResources();
      MessagePlugin.success('服务资源已删除');
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, '删除服务资源失败'));
    } finally {
      setDeleting(false);
    }
  };

  const columns: TableProps<ServiceResource>['columns'] = [
    {
      colKey: 'displayName',
      title: '显示名称',
      minWidth: 180,
      cell: ({ row }) => <div className="permission-table-name">{row.displayName || row.name || row.key || '-'}</div>,
    },
    { colKey: 'key', title: '唯一 Key', minWidth: 180, className: 'permission-table-code', cell: ({ row }) => row.key || '-' },
    { colKey: 'audience', title: 'Audience', minWidth: 170, className: 'permission-table-code', cell: ({ row }) => row.audience || '-' },
    { colKey: 'source', title: '来源', width: 100, cell: ({ row }) => <Tag theme={sourceTheme(row.source)} variant="light-outline">{sourceLabel(row.source)}</Tag> },
    { colKey: 'description', title: '描述', minWidth: 180, className: 'permission-table-muted', cell: ({ row }) => row.description || '-' },
    { colKey: 'isActive', title: '状态', width: 90, cell: ({ row }) => <Tag theme={row.isActive ? 'success' : 'default'} variant="light-outline">{row.isActive ? '启用' : '停用'}</Tag> },
    { colKey: 'createdAt', title: '创建时间', minWidth: 170, className: 'permission-table-muted', cell: ({ row }) => formatDateTime(row.createdAt) },
    {
      colKey: 'actions',
      title: '操作',
      minWidth: 150,
      cell: ({ row }) => serviceResourcesWritable ? (
        <Space size="small">
          <Button variant="text" theme="primary" type="button" onClick={() => openEditDialog(row)}>编辑</Button>
          <Button variant="text" theme="danger" type="button" onClick={() => setDeleteTarget(row)}>删除</Button>
        </Space>
      ) : <span className="permission-table-muted">只读</span>,
    },
  ];

  const tableEmpty = serviceResourcesError ? (
    <Empty
      type="fail"
      title={serviceResourcesError}
      action={<Button variant="text" theme="primary" type="button" onClick={() => void reloadServiceResources()}>重试</Button>}
    />
  ) : <Empty description="暂无服务资源" />;

  return (
    <div className="permission-page permission-service-resource-page">
      <PageHeader
        title="服务资源管理"
        actions={serviceResourcesWritable ? <Button theme="primary" type="button" onClick={openCreateDialog}>新建服务资源</Button> : undefined}
      />

      <div className="permission-toolbar">
        <span className="permission-toolbar-meta">共 {serviceResources.length} 个服务资源</span>
        <span className="permission-toolbar-meta">维护模式：{serviceResourcesWritable ? '可编辑' : '只读'}</span>
      </div>

      <Card className="permission-card" bordered>
        <div className="permission-table-wrap">
          <Table<ServiceResource>
            className="permission-table permission-service-resource-table"
            rowKey="key"
            data={serviceResourcesLoading || serviceResourcesError ? [] : serviceResources}
            columns={columns}
            loading={serviceResourcesLoading ? <Loading text="正在加载服务资源..." /> : false}
            hover
            tableLayout="auto"
            empty={tableEmpty}
          />
        </div>
      </Card>

      <Dialog
        visible={dialogVisible}
        header={editingResource ? '编辑服务资源' : '新建服务资源'}
        width={600}
        confirmBtn={{ content: editingResource ? '保存' : '创建', theme: 'primary', loading: saving }}
        cancelBtn="取消"
        confirmLoading={saving}
        onConfirm={() => void submitForm()}
        onCancel={closeDialog}
        onClose={closeDialog}
        destroyOnClose
      >
        <Form className="permission-inline-form" labelAlign="top">
          <Form.FormItem label="唯一 Key">
            <Input
              value={form.key}
              disabled={Boolean(editingResource)}
              maxlength={128}
              placeholder="例如 content-platform"
              onChange={(value) => setForm((prev) => ({ ...prev, key: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="显示名称">
            <Input
              value={form.displayName}
              maxlength={100}
              placeholder="例如 内容平台"
              onChange={(value) => setForm((prev) => ({ ...prev, displayName: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="Audience">
            <Input
              value={form.audience}
              maxlength={200}
              placeholder="例如 permission.center.api"
              onChange={(value) => setForm((prev) => ({ ...prev, audience: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="状态">
            <Select
              value={form.isActive ? 'enabled' : 'disabled'}
              options={statusOptions}
              onChange={(value) => setForm((prev) => ({ ...prev, isActive: value === 'enabled' }))}
            />
          </Form.FormItem>
          <Form.FormItem label="描述" className="full-width">
            <Input
              value={form.description}
              maxlength={200}
              placeholder="可选"
              onChange={(value) => setForm((prev) => ({ ...prev, description: value }))}
            />
          </Form.FormItem>
        </Form>
      </Dialog>

      <Dialog
        visible={Boolean(deleteTarget)}
        header="删除服务资源"
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
          确认删除服务资源 <strong>{deleteTarget?.displayName || deleteTarget?.name || deleteTarget?.key || ''}</strong>？删除后该资源下的权限数据将无法继续使用。
        </p>
      </Dialog>
    </div>
  );
};

export default ServiceResourceManagementPage;
