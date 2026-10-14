import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Card, DialogPlugin, Drawer, Form, Input, MessagePlugin, Select, Space, Switch, Table, Tag, Textarea, type TableProps } from 'tdesign-react';
import { AddIcon, DeleteIcon, EditIcon, LinkIcon, LinkUnlinkIcon, RefreshIcon, SearchIcon } from 'tdesign-icons-react';
import { useNavigate } from 'react-router-dom';
import {
  replaceApplicationServiceResources,
  type ApplicationServiceResource,
} from '../../../api/application';
import {
  createServiceResource,
  deleteServiceResource,
  listServiceResources,
  updateServiceResource,
  type CreateServiceResourceRequest,
  type ServiceResource,
  type UpdateServiceResourceRequest,
} from '../../../api/permission';
import { Empty, fail, Guard, Page, type State } from '../shared';
import { getRequestErrorMessage } from '../../nexus-authorization/shared';
import './services.less';

type Filter = 'all' | 'associated';
type ResourceForm = {
  key: string;
  displayName: string;
  audience: string;
  description: string;
  isActive: boolean;
};

const emptyForm: ResourceForm = {
  key: '',
  displayName: '',
  audience: '',
  description: '',
  isActive: true,
};

const resourceLabel = (resource: ServiceResource | ApplicationServiceResource) =>
  resource.displayName || resource.name || resource.key;

const serviceFromApplication = (resource: ApplicationServiceResource): ServiceResource => ({
  id: resource.id,
  key: resource.key,
  name: resource.name,
  displayName: resource.displayName,
  audience: resource.audience,
  description: resource.description,
  isActive: resource.isActive,
  source: resource.source,
});

export default function Services({ state }: { state: State }) {
  const navigate = useNavigate();
  const [catalog, setCatalog] = useState<ServiceResource[]>([]);
  const [writable, setWritable] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [search, setSearch] = useState('');
  const [draftSearch, setDraftSearch] = useState('');
  const [draftFilter, setDraftFilter] = useState<Filter>('all');
  const [filter, setFilter] = useState<Filter>('all');
  const [associationSaving, setAssociationSaving] = useState<string | null>(null);
  const [associationError, setAssociationError] = useState('');
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<string>();
  const [form, setForm] = useState<ResourceForm>({ ...emptyForm });
  const [editorSaving, setEditorSaving] = useState(false);
  const [deletingKey, setDeletingKey] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState('');
  const [editorError, setEditorError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<{ key?: string; displayName?: string }>({});

  const associatedKeys = useMemo(() => new Set(state.services.map(item => item.key)), [state.services]);
  const resources = useMemo(() => {
    const values = new Map(catalog.map(item => [item.key, item]));
    state.services.forEach(item => {
      if (!values.has(item.key)) values.set(item.key, serviceFromApplication(item));
    });
    return Array.from(values.values());
  }, [catalog, state.services]);

  const loadCatalog = async () => {
    setLoading(true);
    setLoadError('');
    try {
      const result = await listServiceResources();
      setCatalog(result.items);
      setWritable(result.writable);
    } catch (error) {
      setLoadError('加载服务目录失败');
      fail(error, '加载服务目录失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void loadCatalog(); }, []);

  const visibleResources = useMemo(() => {
    const query = search.trim().toLowerCase();
    return resources.filter(item => {
      if (filter === 'associated' && !associatedKeys.has(item.key)) return false;
      if (!query) return true;
      return `${resourceLabel(item)} ${item.key} ${item.audience}`.toLowerCase().includes(query);
    });
  }, [associatedKeys, filter, resources, search]);

  const updateAssociation = async (key: string, shouldAssociate: boolean) => {
    if (associationSaving || !state.appKey) return;
    const next = new Set(associatedKeys);
    if (shouldAssociate) next.add(key);
    else next.delete(key);
    setAssociationSaving(key);
    setAssociationError('');
    try {
      await replaceApplicationServiceResources(state.appKey, Array.from(next));
      await state.reload();
    } catch (error) {
      setAssociationError(getRequestErrorMessage(error, '服务关联保存失败，请重试。'));
      fail(error, '服务关联保存失败');
    } finally {
      setAssociationSaving(null);
    }
  };

  const openCreate = () => {
    if (!writable) return;
    setEditingKey(undefined);
    setForm({ ...emptyForm });
    setFieldErrors({});
    setEditorError('');
    setEditorOpen(true);
  };

  const openEdit = (item: ServiceResource) => {
    if (!writable || item.source !== 'local') return;
    setEditingKey(item.key);
    setForm({
      key: item.key,
      displayName: item.displayName || item.name || item.key,
      audience: item.audience,
      description: item.description,
      isActive: item.isActive,
    });
    setFieldErrors({});
    setEditorError('');
    setEditorOpen(true);
  };

  const closeEditor = () => {
    if (editorSaving) return;
    setEditorOpen(false);
  };

  const saveEditor = async () => {
    if (editorSaving || !writable) return;
    const displayName = form.displayName.trim();
    const key = form.key.trim();
    const errors: typeof fieldErrors = {};
    if (!displayName) errors.displayName = '请输入服务名称';
    if (!key) errors.key = '请输入服务标识';
    setFieldErrors(errors);
    if (Object.keys(errors).length) return;
    setEditorSaving(true);
    setEditorError('');
    try {
      if (editingKey) {
        const payload: UpdateServiceResourceRequest = {
          display_name: displayName,
          audience: form.audience.trim(),
          description: form.description.trim(),
          is_active: form.isActive,
        };
        await updateServiceResource(editingKey, payload);
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
      await loadCatalog();
      await state.reload();
      setEditorOpen(false);
    } catch (error) {
      setEditorError('保存服务资源失败，请检查输入后重试。');
      fail(error, '保存服务资源失败');
    } finally {
      setEditorSaving(false);
    }
  };

  const setFormField = <K extends keyof ResourceForm>(field: K, value: ResourceForm[K]) => {
    setForm(current => ({ ...current, [field]: value }));
    if (field === 'key' || field === 'displayName') {
      setFieldErrors(current => ({ ...current, [field]: undefined }));
    }
    setEditorError('');
  };

  const canEdit = writable;

  const confirmDelete = (item: ServiceResource) => {
    if (!writable || item.source !== 'local' || deletingKey || associatedKeys.has(item.key)) return;
    setDeleteError('');
    let deleting = false;
    const dialog = DialogPlugin.confirm({
      header: '删除服务资源',
      body: `确认删除“${resourceLabel(item)}”吗？删除后无法恢复。请先解除所有应用关联并删除该服务下的 API 接口。`,
      theme: 'warning',
      confirmBtn: { content: '确认删除', theme: 'danger' },
      cancelBtn: '取消',
      closeOnOverlayClick: false,
      onConfirm: async () => {
        if (deleting) return;
        deleting = true;
        setDeletingKey(item.key);
        dialog.update({ confirmBtn: { content: '正在删除', theme: 'danger', loading: true }, cancelBtn: { content: '取消', disabled: true }, closeOnEscKeydown: false });
        try {
          await deleteServiceResource(item.key);
          dialog.destroy();
          await loadCatalog();
          await state.reload();
          MessagePlugin.success('服务资源已删除');
        } catch (error) {
          setDeleteError(getRequestErrorMessage(error, '删除失败，请先解除所有应用关联并删除该服务下的 API 接口。'));
          fail(error, '删除服务资源失败');
          dialog.update({ confirmBtn: { content: '确认删除', theme: 'danger', loading: false }, cancelBtn: '取消', closeOnEscKeydown: true });
        } finally {
          deleting = false;
          setDeletingKey(null);
        }
      },
      onCancel: () => { if (!deleting) dialog.destroy(); },
      onClose: () => { if (!deleting) dialog.destroy(); },
    });
  };

  const columns: TableProps<ServiceResource>['columns'] = [
    { colKey: 'displayName', title: '服务名称 / 标识', width: 280, cell: ({ row }) => <div className="resource-services-name-cell" title={row.description || undefined}><strong>{resourceLabel(row)}</strong><code>{row.key}</code></div> },
    { colKey: 'audience', title: 'Audience', width: 200, cell: ({ row }) => <code>{row.audience || '-'}</code> },
    { colKey: 'isActive', title: '状态', width: 110, cell: ({ row }) => <Tag theme={row.isActive ? 'success' : 'default'} variant="light-outline">{row.isActive ? '启用' : '停用'}</Tag> },
    { colKey: 'associated', title: '应用关联', width: 140, cell: ({ row }) => <Tag theme={associatedKeys.has(row.key) ? 'primary' : 'default'} variant="light-outline">{associatedKeys.has(row.key) ? '已关联' : '未关联'}</Tag> },
    { colKey: 'actions', title: '操作', width: 350, fixed: 'right', cell: ({ row }) => {
      const associated = associatedKeys.has(row.key);
      const editable = canEdit && row.source === 'local';
      return <Space className="resource-services-row-actions" size={0}>
        <Button variant="text" disabled={!associated || !row.isActive} title={associated ? '查看该服务的 API' : '请先关联服务'} onClick={() => navigate(`/api-resources?service=${encodeURIComponent(row.key)}`)}>查看 API</Button>
        {editable ? <>
          <Button variant="text" icon={<EditIcon />} disabled={Boolean(deletingKey)} onClick={() => openEdit(row)}>编辑</Button>
          <Button variant="text" theme="danger" icon={<DeleteIcon />} disabled={associated || Boolean(deletingKey)} loading={deletingKey === row.key} title={associated ? '请先取消当前应用的服务关联' : '删除本地服务资源'} onClick={() => confirmDelete(row)}>删除</Button>
        </> : null}
        {associated ? <Button variant="text" theme="danger" loading={associationSaving === row.key} disabled={Boolean(associationSaving)} icon={<LinkUnlinkIcon />} onClick={() => void updateAssociation(row.key, false)}>取消关联</Button> : <Button variant="text" disabled={!row.isActive || Boolean(associationSaving)} loading={associationSaving === row.key} icon={<LinkIcon />} onClick={() => void updateAssociation(row.key, true)}>关联</Button>}
      </Space>;
    } },
  ];

  return <Guard state={state}><Page
    title="服务资源"
    actions={canEdit ? <Button theme="primary" icon={<AddIcon />} onClick={openCreate}>新建服务</Button> : null}
  >
    <div className="page-filter-bar">
      <Form className="workbench-filter-form" layout="inline" colon={false} labelWidth={100} onSubmit={() => { setSearch(draftSearch); setFilter(draftFilter); }}>
        <Form.FormItem label="关键词"><><Input className="workbench-keyword" value={draftSearch} clearable prefixIcon={<SearchIcon />} placeholder="服务名称、标识或 Audience" onChange={setDraftSearch} /></></Form.FormItem>
        <Form.FormItem label="关联状态"><><Select className="workbench-select" value={draftFilter === 'associated' ? 'associated' : ''} placeholder="全部服务" clearable options={[{ label: '已关联服务', value: 'associated' }]} onChange={value => setDraftFilter(value ? 'associated' : 'all')} /></></Form.FormItem>
        <Form.FormItem><Space size={16}><Button theme="primary" type="submit" icon={<SearchIcon />}>查询</Button><Button theme="primary" icon={<RefreshIcon />} onClick={() => { setDraftSearch(''); setSearch(''); setDraftFilter('all'); setFilter('all'); }}>重置</Button></Space></Form.FormItem>
      </Form>
    </div>
    {associationError ? <Alert className="resource-services-notice" theme="error" message={associationError} /> : null}
    {deleteError ? <Alert className="resource-services-error" theme="error" message={deleteError} /> : null}
    <Card className="resource-services-table-card">
      {loadError ? <Alert theme="error" title="加载服务目录失败" message={loadError} operation={<Button variant="outline" onClick={() => void loadCatalog()}>重试</Button>} /> :
        <Table<ServiceResource> className="resource-services-table" rowKey="key" data={visibleResources} columns={columns} loading={loading} hover tableLayout="auto" empty={<Empty text={resources.length ? '没有匹配的服务资源' : '暂无服务资源'} />} />}
      <div className="resource-services-footer"><span>共 {visibleResources.length} 条</span></div>
    </Card>
    <Drawer className="resource-services-drawer" visible={editorOpen} header={editingKey ? '编辑服务资源' : '新建服务资源'} size="560px" closeOnOverlayClick={false} closeOnEscKeydown={!editorSaving} footer={<Space className="resource-services-drawer-footer"><Button variant="outline" disabled={editorSaving} onClick={closeEditor}>取消</Button><Button theme="primary" loading={editorSaving} disabled={editorSaving || !canEdit} onClick={() => void saveEditor()}>{editingKey ? '保存修改' : '创建服务'}</Button></Space>} onClose={closeEditor}>
      <Form className="resource-services-form" labelAlign="top" colon={false} onSubmit={() => void saveEditor()}>
        <Form.FormItem label="服务名称" requiredMark help={fieldErrors.displayName || '便于团队识别。'} status={fieldErrors.displayName ? 'error' : undefined}><><Input value={form.displayName} disabled={editorSaving || !canEdit} onChange={value => setFormField('displayName', value)} /></></Form.FormItem>
        <Form.FormItem label="服务标识" requiredMark help={fieldErrors.key || '用于 API 和权限数据关联，创建后不可修改。'} status={fieldErrors.key ? 'error' : undefined}><><Input value={form.key} disabled={Boolean(editingKey) || editorSaving || !canEdit} onChange={value => setFormField('key', value)} /></></Form.FormItem>
        <Form.FormItem label="Audience"><><Input value={form.audience} placeholder="例如：orders" disabled={editorSaving || !canEdit} onChange={value => setFormField('audience', value)} /></></Form.FormItem>
        <Form.FormItem label="描述"><><Textarea value={form.description} placeholder="简要说明服务用途（选填）" disabled={editorSaving || !canEdit} onChange={value => setFormField('description', value)} /></></Form.FormItem>
        <Form.FormItem label="启用服务" help="停用前请先解除所有应用关联；停用后不可新增关联。"><><Switch aria-label="启用服务" value={form.isActive} disabled={editorSaving || !canEdit} onChange={value => setFormField('isActive', Boolean(value))} /></></Form.FormItem>
        {!canEdit ? <Alert theme="info" message="当前服务目录由 NexusAuth 提供，资源为只读。" /> : null}
        {editorError ? <Alert theme="error" message={editorError} /> : null}
      </Form>
    </Drawer>
  </Page></Guard>;
}
