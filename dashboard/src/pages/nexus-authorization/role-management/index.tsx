import { useCallback, useEffect, useState } from 'react';
import { Button, Card, Checkbox, Dialog, Drawer, Empty, Form, Input, Loading, MessagePlugin, Select, Space, Table, Tag, type TableProps } from 'tdesign-react';

import {
  createRole,
  deleteRole,
  getUserRoleIDs,
  listRoles,
  replaceUserRoles,
  updateRole,
  type Role,
} from '../../../api/permission';
import { formatDateTime, getRequestErrorMessage, PageHeader, useServiceResourceScope } from '../shared';
import '../style.less';

type RoleForm = {
  code: string;
  name: string;
  description: string;
  enabled: boolean;
};

const EMPTY_ROLE_FORM: RoleForm = { code: '', name: '', description: '', enabled: true };

const statusOptions = [
  { label: '启用', value: 'enabled' },
  { label: '停用', value: 'disabled' },
];

const RoleManagementPage = () => {
  const { serviceResource } = useServiceResourceScope();
  const [roles, setRoles] = useState<Role[]>([]);
  const [loading, setLoading] = useState(false);
  const [dialogVisible, setDialogVisible] = useState(false);
  const [roleForm, setRoleForm] = useState<RoleForm>(EMPTY_ROLE_FORM);
  const [editingRole, setEditingRole] = useState<Role | null>(null);
  const [savingRole, setSavingRole] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Role | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [assignmentVisible, setAssignmentVisible] = useState(false);
  const [assignmentRole, setAssignmentRole] = useState<Role | null>(null);
  const [assignmentSubject, setAssignmentSubject] = useState('');
  const [queriedAssignmentSubject, setQueriedAssignmentSubject] = useState('');
  const [assignmentRoleIDs, setAssignmentRoleIDs] = useState<string[]>([]);
  const [assignmentRoleSelected, setAssignmentRoleSelected] = useState(false);
  const [assignmentLoading, setAssignmentLoading] = useState(false);
  const [savingAssignment, setSavingAssignment] = useState(false);

  const loadRoles = useCallback(async (scope: string) => {
    setLoading(true);
    try {
      setRoles(await listRoles(scope));
    } catch (error) {
      setRoles([]);
      MessagePlugin.error(getRequestErrorMessage(error, '加载角色失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadRoles(serviceResource);
  }, [serviceResource, loadRoles]);

  const resetRoleDialog = () => {
    setEditingRole(null);
    setRoleForm({ ...EMPTY_ROLE_FORM });
  };

  const openCreateDialog = () => {
    resetRoleDialog();
    setDialogVisible(true);
  };

  const openEditDialog = (role: Role) => {
    setEditingRole(role);
    setRoleForm({
      code: role.code,
      name: role.name,
      description: role.description,
      enabled: role.enabled,
    });
    setDialogVisible(true);
  };

  const closeRoleDialog = () => {
    if (savingRole) {
      return;
    }
    setDialogVisible(false);
    resetRoleDialog();
  };

  const submitRole = async () => {
    const code = roleForm.code.trim();
    const name = roleForm.name.trim();
    if (!code || !name) {
      MessagePlugin.warning('请填写角色编码和角色名称');
      return;
    }

    const editing = editingRole;
    setSavingRole(true);
    try {
      if (editing) {
        const updated = await updateRole(editing.id, {
          code,
          name,
          description: roleForm.description.trim(),
          enabled: roleForm.enabled,
        });
        setRoles((current) => current.map((role) => role.id === updated.id ? updated : role));
      } else {
        const created = await createRole({ service_resource: serviceResource, code, name, description: roleForm.description.trim() });
        setRoles((current) => [...current, created]);
      }
      setDialogVisible(false);
      resetRoleDialog();
      MessagePlugin.success(editing ? '角色已更新' : '角色已创建');
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, editing ? '更新角色失败' : '创建角色失败'));
    } finally {
      setSavingRole(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) {
      return;
    }
    const target = deleteTarget;
    setDeleting(true);
    try {
      await deleteRole(target.id);
      setRoles((current) => current.filter((role) => role.id !== target.id));
      setDeleteTarget(null);
      MessagePlugin.success('角色已删除');
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, '删除角色失败'));
    } finally {
      setDeleting(false);
    }
  };

  const resetAssignmentDrawer = () => {
    setAssignmentVisible(false);
    setAssignmentRole(null);
    setAssignmentSubject('');
    setQueriedAssignmentSubject('');
    setAssignmentRoleIDs([]);
    setAssignmentRoleSelected(false);
  };

  const openAssignmentDrawer = (role: Role) => {
    if (assignmentLoading || savingAssignment) {
      return;
    }
    setAssignmentRole(role);
    setAssignmentSubject('');
    setQueriedAssignmentSubject('');
    setAssignmentRoleIDs([]);
    setAssignmentRoleSelected(false);
    setAssignmentVisible(true);
  };

  const closeAssignmentDrawer = () => {
    if (assignmentLoading || savingAssignment) {
      return;
    }
    resetAssignmentDrawer();
  };

  const changeAssignmentSubject = (value: string) => {
    setAssignmentSubject(value);
    setQueriedAssignmentSubject('');
    setAssignmentRoleIDs([]);
    setAssignmentRoleSelected(false);
  };

  const queryAssignment = async () => {
    if (assignmentLoading || savingAssignment || !assignmentRole) {
      return;
    }
    const subject = assignmentSubject.trim();
    if (!subject) {
      MessagePlugin.warning('请输入 NexusAuth subject');
      return;
    }

    setAssignmentLoading(true);
    try {
      const roleIDs = Array.from(new Set(await getUserRoleIDs(subject, assignmentRole.serviceResource)));
      setAssignmentRoleIDs(roleIDs);
      setQueriedAssignmentSubject(subject);
      setAssignmentRoleSelected(roleIDs.includes(assignmentRole.id));
    } catch (error) {
      setQueriedAssignmentSubject('');
      setAssignmentRoleIDs([]);
      setAssignmentRoleSelected(false);
      MessagePlugin.error(getRequestErrorMessage(error, '查询用户角色失败'));
    } finally {
      setAssignmentLoading(false);
    }
  };

  const saveAssignment = async () => {
    if (assignmentLoading || savingAssignment) {
      return;
    }
    const role = assignmentRole;
    const subject = assignmentSubject.trim();
    if (!role || !subject || queriedAssignmentSubject !== subject) {
      MessagePlugin.warning('请先查询用户角色');
      return;
    }

    const nextRoleIDs = new Set(assignmentRoleIDs);
    if (assignmentRoleSelected) {
      nextRoleIDs.add(role.id);
    } else {
      nextRoleIDs.delete(role.id);
    }

    setSavingAssignment(true);
    try {
      await replaceUserRoles(subject, role.serviceResource, Array.from(nextRoleIDs));
      await getUserRoleIDs(subject, role.serviceResource);
      resetAssignmentDrawer();
      MessagePlugin.success('用户分配已保存');
    } catch (error) {
      MessagePlugin.error(getRequestErrorMessage(error, '保存用户分配失败'));
    } finally {
      setSavingAssignment(false);
    }
  };

  const columns: TableProps<Role>['columns'] = [
    {
      colKey: 'name',
      title: '角色名称',
      minWidth: 180,
      cell: ({ row }) => (
        <>
          <div className="permission-table-name">{row.name || '-'}</div>
          <div className="permission-table-code">{row.id || '-'}</div>
        </>
      ),
    },
    { colKey: 'code', title: '角色编码', minWidth: 150, className: 'permission-table-code' },
    { colKey: 'description', title: '描述', minWidth: 180, className: 'permission-table-muted', cell: ({ row }) => row.description || '-' },
    { colKey: 'createdAt', title: '创建时间', minWidth: 170, className: 'permission-table-muted', cell: ({ row }) => formatDateTime(row.createdAt) },
    {
      colKey: 'enabled',
      title: '状态',
      width: 90,
      cell: ({ row }) => <Tag theme={row.enabled ? 'success' : 'default'} variant="light-outline">{row.enabled ? '启用' : '停用'}</Tag>,
    },
    {
      colKey: 'actions',
      title: '操作',
      minWidth: 230,
      cell: ({ row }) => (
        <Space size="small">
          <Button variant="text" theme="primary" type="button" onClick={() => openEditDialog(row)}>编辑</Button>
          <Button variant="text" theme="danger" type="button" onClick={() => setDeleteTarget(row)}>删除</Button>
          <Button variant="text" theme="primary" type="button" disabled={!row.enabled} onClick={() => openAssignmentDrawer(row)}>分配用户</Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="permission-page permission-role-page">
      <PageHeader
        title="角色管理"
        actions={<Button theme="primary" type="button" onClick={openCreateDialog}>新建角色</Button>}
      />

      <Card className="permission-card" bordered>
        <div className="permission-table-wrap">
          <Table<Role>
            className="permission-table"
            rowKey="id"
            data={loading ? [] : roles}
            columns={columns}
            loading={loading ? <Loading text="正在加载角色..." /> : false}
            hover
            tableLayout="auto"
            empty={<Empty description="当前服务资源暂无角色" />}
          />
        </div>
      </Card>

      <Dialog
        visible={dialogVisible}
        header={editingRole ? '编辑角色' : '新建角色'}
        width={520}
        confirmBtn={{ content: editingRole ? '保存' : '创建', theme: 'primary', loading: savingRole }}
        cancelBtn="取消"
        confirmLoading={savingRole}
        onConfirm={() => void submitRole()}
        onCancel={closeRoleDialog}
        onClose={closeRoleDialog}
        destroyOnClose
      >
        <Form className="permission-drawer-form" labelAlign="top">
          <Form.FormItem label="角色编码" help="建议使用稳定、便于接口传递的英文编码。">
            <Input
              value={roleForm.code}
              placeholder="例如 content-editor"
              maxlength={100}
              onChange={(value) => setRoleForm((prev) => ({ ...prev, code: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="角色名称">
            <Input
              value={roleForm.name}
              placeholder="例如 内容编辑"
              maxlength={100}
              onChange={(value) => setRoleForm((prev) => ({ ...prev, name: value }))}
            />
          </Form.FormItem>
          <Form.FormItem label="描述">
            <Input
              value={roleForm.description}
              placeholder="可选"
              maxlength={200}
              onChange={(value) => setRoleForm((prev) => ({ ...prev, description: value }))}
            />
          </Form.FormItem>
          {editingRole ? (
            <Form.FormItem label="启用状态">
              <Select
                value={roleForm.enabled ? 'enabled' : 'disabled'}
                options={statusOptions}
                onChange={(value) => setRoleForm((prev) => ({ ...prev, enabled: value === 'enabled' }))}
              />
            </Form.FormItem>
          ) : null}
        </Form>
      </Dialog>

      <Dialog
        visible={Boolean(deleteTarget)}
        header="删除角色"
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
          确认删除角色 <strong>{deleteTarget?.name || deleteTarget?.code || ''}</strong>？删除后该角色的用户绑定也会失效。
        </p>
      </Dialog>

      <Drawer
        visible={assignmentVisible}
        header={assignmentRole ? `分配用户 · ${assignmentRole.name}` : '分配用户'}
        size="520px"
        confirmBtn={{
          content: '保存分配',
          theme: 'primary',
          loading: savingAssignment,
          disabled: !queriedAssignmentSubject || queriedAssignmentSubject !== assignmentSubject.trim() || assignmentLoading,
        }}
        cancelBtn="取消"
        onConfirm={() => void saveAssignment()}
        onCancel={closeAssignmentDrawer}
        onClose={closeAssignmentDrawer}
        destroyOnClose
      >
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Space>
            <Input
              value={assignmentSubject}
              placeholder="输入 NexusAuth subject"
              clearable
              disabled={assignmentLoading || savingAssignment}
              onChange={changeAssignmentSubject}
              onEnter={() => void queryAssignment()}
            />
            <Button
              theme="primary"
              type="button"
              loading={assignmentLoading}
              disabled={savingAssignment}
              onClick={() => void queryAssignment()}
            >查询</Button>
          </Space>
          {queriedAssignmentSubject ? (
            <Checkbox
              checked={assignmentRoleSelected}
              disabled={savingAssignment}
              onChange={(checked) => setAssignmentRoleSelected(checked)}
            >
              分配当前角色
            </Checkbox>
          ) : (
            <Empty description="输入 NexusAuth subject 后查询当前用户的角色分配。" />
          )}
        </Space>
      </Drawer>
    </div>
  );
};

export default RoleManagementPage;
