import { useEffect, useState } from 'react';
import { Alert, Button, Drawer, Form, MessagePlugin, Select } from 'tdesign-react';
import { updateRoleSubjects, type Role } from '../../../api/rbac-v1';
import { listAllUsers, scimUserLabel, type SCIMUser } from '../../../api/user';
import { getRequestErrorMessage } from '../../nexus-authorization/shared';
import type { State } from '../shared';

// The default domain is the empty string in the database. Legacy rows still use the "global"
// alias, which reads as the same default domain; writes always target the empty default domain.
const DOMAIN = '';
const isDefaultDomain = (domain: string) => domain === DOMAIN || domain === 'global';

export default function RoleUsers({ state, role, onClose }: { state: State; role: Role; onClose: () => void }) {
  const [originalIDs, setOriginalIDs] = useState(() => new Set(state.bindings
    .filter(binding => binding.role_id === role.id && isDefaultDomain(binding.domain))
    .map(binding => binding.subject)));
  const [selectedIDs, setSelectedIDs] = useState<string[]>(() => [...originalIDs]);
  const [users, setUsers] = useState<SCIMUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [usersError, setUsersError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [retry, setRetry] = useState(0);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setUsersError('');
    void listAllUsers(controller.signal).then(values => {
      if (!controller.signal.aborted) setUsers(values);
    }).catch(error => {
      if (!controller.signal.aborted) {
        setUsers([]);
        setUsersError(getRequestErrorMessage(error, '加载用户列表失败'));
      }
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [retry]);

  const userByID = new Map(users.map(user => [user.id, user]));
  const blocked = saving || loading || Boolean(usersError) || state.loading || Boolean(state.loadError);
  const options = users.map(user => ({
    label: `${scimUserLabel(user)}${user.active === false ? ' · 已停用' : ''}`,
    value: user.id,
    disabled: (!role.enabled || user.active === false) && !originalIDs.has(user.id),
  }));
  for (const subject of originalIDs) {
    if (!userByID.has(subject)) options.push({ label: `原绑定用户（${subject}）`, value: subject, disabled: false });
  }

  const close = () => { if (!saving) onClose(); };
  const save = async () => {
    if (blocked) return;
    const selected = new Set(selectedIDs);
    const added = selectedIDs.filter(subject => !originalIDs.has(subject));
    const removed = [...originalIDs].filter(subject => !selected.has(subject));
    if (!added.length && !removed.length) { onClose(); return; }
    if (added.some(subject => !role.enabled || !userByID.has(subject) || userByID.get(subject)?.active === false)) {
      setSaveError('只能为启用的角色分配有效且启用的用户');
      return;
    }
    setSaving(true);
    setSaveError('');
    try {
      // One atomic request that only grants or revokes this role: the users' other roles and
      // concurrent edits of other roles are never overwritten.
      await updateRoleSubjects(state.appKey, role.id, DOMAIN, added, removed);
      setOriginalIDs(selected);
      await state.reload();
      MessagePlugin.success('用户分配已保存');
      onClose();
    } catch (error) {
      // PUBLISH_PENDING: the assignment is saved; only its publication is retried in the background.
      if ((error as { code?: string })?.code === 'PUBLISH_PENDING') {
        setOriginalIDs(selected);
        void state.reload();
      }
      setSaveError(getRequestErrorMessage(error, '保存用户分配失败'));
    } finally {
      setSaving(false);
    }
  };

  return <Drawer
    className="rbac-drawer"
    visible
    header={`分配用户 · ${role.name}`}
    size="520px"
    confirmBtn={{ content: '保存分配', tag: 'button', loading: saving, disabled: blocked }}
    cancelBtn={{ content: '取消', tag: 'button', disabled: saving }}
    onConfirm={() => void save()}
    onClose={close}
    onCancel={close}
    closeOnOverlayClick={false}
    closeOnEscKeydown={!saving}
  >
    {usersError ? <div role="alert"><Alert theme="error" message={usersError} operation={<Button variant="text" onClick={() => { setLoading(true); setRetry(value => value + 1); }}>重试</Button>} /></div> : null}
    {saveError ? <div role="alert"><Alert theme="error" message={saveError} /></div> : null}
    <Form className="rbac-form" labelAlign="top" colon={false}>
      <Form.FormItem label="用户" help="选中的用户拥有此角色，取消选择后会移除此角色，保留其其他角色。">
        <div style={{ width: '100%' }}><Select
          style={{ width: '100%' }}
          multiple
          filterable
          loading={loading}
          loadingText="正在加载用户..."
          disabled={blocked}
          value={selectedIDs}
          placeholder="搜索并选择用户"
          options={options}
          empty="没有可选用户"
          onChange={value => { setSelectedIDs((value as string[]).map(String)); setSaveError(''); }}
        /></div>
      </Form.FormItem>
    </Form>
  </Drawer>;
}
