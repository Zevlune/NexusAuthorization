import { useEffect, useState } from 'react';
import { Alert, Button, Drawer, Form, MessagePlugin, Select } from 'tdesign-react';
import { listSubjectRoles, replaceSubjectRoles, type Role } from '../../../api/rbac-v1';
import { listAllUsers, type SCIMUser } from '../../../api/user';
import { getRequestErrorMessage } from '../../nexus-authorization/shared';
import type { State } from '../shared';

const DOMAIN = 'global';

const userLabel = (user: SCIMUser) => {
  const name = user.name?.formatted || [user.name?.givenName, user.name?.familyName].filter(Boolean).join(' ');
  const email = (user.emails?.find(item => item.primary) || user.emails?.[0])?.value;
  return [name && name !== user.userName ? name : '', user.userName, email].filter(Boolean).join(' · ') || user.id;
};

export default function RoleUsers({ state, role, onClose }: { state: State; role: Role; onClose: () => void }) {
  const [originalIDs, setOriginalIDs] = useState(() => new Set(state.bindings
    .filter(binding => binding.role_id === role.id && binding.domain === DOMAIN)
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
    label: `${userLabel(user)}${user.active === false ? ' · 已停用' : ''}`,
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
    const changedSubjects = [...new Set([...originalIDs, ...selected])]
      .filter(subject => originalIDs.has(subject) !== selected.has(subject));
    if (!changedSubjects.length) { onClose(); return; }
    if (changedSubjects.some(subject => selected.has(subject)
      && (!role.enabled || !userByID.has(subject) || userByID.get(subject)?.active === false))) {
      setSaveError('只能为启用的角色分配有效且启用的用户');
      return;
    }
    setSaving(true);
    setSaveError('');
    try {
      // Refresh before merging so unrelated roles added since opening remain assigned.
      const currentBindings = (await listSubjectRoles(state.appKey)).items || [];
      const outcomes = await Promise.allSettled(changedSubjects.map(subject => {
        const otherRoles = currentBindings
          .filter(binding => binding.subject === subject && binding.domain === DOMAIN && binding.role_id !== role.id)
          .map(binding => binding.role_id);
        const roleIDs = [...new Set(selected.has(subject) ? [...otherRoles, role.id] : otherRoles)];
        return replaceSubjectRoles(state.appKey, subject, DOMAIN, roleIDs);
      }));
      // Keep completed changes when retrying a partially successful batch.
      const committed = new Set(originalIDs);
      outcomes.forEach((outcome, index) => {
        if (outcome.status !== 'fulfilled') return;
        const subject = changedSubjects[index];
        if (selected.has(subject)) committed.add(subject);
        else committed.delete(subject);
      });
      setOriginalIDs(committed);
      await state.reload();
      const failures = outcomes.filter(outcome => outcome.status === 'rejected').length;
      if (failures) {
        setSaveError(`${failures === outcomes.length ? '保存用户分配失败' : '部分用户分配保存失败'}，${failures} 个用户未保存。请重试；已保存的分配会保留。`);
        return;
      }
      MessagePlugin.success('用户分配已保存');
      onClose();
    } catch (error) {
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
