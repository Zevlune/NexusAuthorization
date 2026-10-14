import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Drawer,
  Empty,
  Form,
  Input,
  Loading,
  MessagePlugin,
  Radio,
  Space,
  Statistic,
  Switch,
  Tag,
  Textarea,
  type InputRef,
} from 'tdesign-react';
import { AddIcon, ArrowRightIcon, EditIcon, MoonIcon, SearchIcon, SunnyIcon } from 'tdesign-icons-react';
import { useLocation, useNavigate } from 'react-router-dom';
import {
  getStoredApplication,
  listApplications,
  saveApplication,
  setStoredApplication,
  type Application,
} from '../../api/application';
import { getPostServiceResourceSelectionPath } from '../../router/auth';
import { applyThemeMode, getThemeMode } from '../../theme';
import { getRequestErrorMessage } from '../nexus-authorization/shared';
import './style.less';

const empty: Application = { key: '', displayName: '', description: '', enabled: true, url: '' };
type FieldName = 'displayName' | 'key' | 'url';
type FieldErrors = Partial<Record<FieldName, string>>;

const getApplicationLabel = (item: Application) => item.displayName.trim() || item.key;

const isValidApplicationUrl = (value: string | undefined): boolean => {
  const url = value?.trim() || '';
  if (!url || Array.from(url).length > 2048 || !/^https?:\/\//.test(url) || /[\\\s]/.test(url)) return false;
  try {
    const parsed = new URL(url);
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:')
      && Boolean(parsed.hostname)
      && !parsed.username
      && !parsed.password;
  } catch {
    return false;
  }
};

export default function SelectApplication() {
  const navigate = useNavigate();
  const location = useLocation();
  const [items, setItems] = useState<Application[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [selected, setSelected] = useState(() => getStoredApplication());
  const [search, setSearch] = useState('');
  const [form, setForm] = useState<Application>({ ...empty });
  const [editingKey, setEditingKey] = useState<string>();
  const [drawer, setDrawer] = useState(false);
  const nameInput = useRef<InputRef>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [themeMode, setThemeMode] = useState<'light' | 'dark'>(() => getThemeMode());

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    try {
      const next = await listApplications();
      setItems(next);
      setSelected(current => {
        const currentItem = next.find(item => item.key === current);
        return currentItem?.enabled ? current : '';
      });
    } catch (error) {
      setLoadError(getRequestErrorMessage(error, '加载应用失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (!drawer) return;
    // Drawer focuses its container after the opening animation.
    const timer = window.setTimeout(() => nameInput.current?.focus(), 350);
    return () => window.clearTimeout(timer);
  }, [drawer]);

  const enabledCount = useMemo(() => items.filter(item => item.enabled).length, [items]);
  const normalizedSearch = search.trim().toLowerCase();
  const visibleItems = useMemo(() => items.filter(item => {
    if (!normalizedSearch) return true;
    return `${item.displayName} ${item.key}`.toLowerCase().includes(normalizedSearch);
  }), [items, normalizedSearch]);
  const selectedItem = items.find(item => item.key === selected && item.enabled);

  const continueToApp = () => {
    if (!selectedItem) return;
    setStoredApplication(selectedItem.key);
    navigate(getPostServiceResourceSelectionPath(new URLSearchParams(location.search).get('redirect')));
  };

  const validate = (): FieldErrors => {
    const errors: FieldErrors = {};
    const displayName = form.displayName.trim();
    const key = form.key.trim();
    const url = form.url?.trim() || '';
    if (!displayName) errors.displayName = '请输入应用名称';
    if (!key) errors.key = '请输入应用标识';
    if (key && !editingKey && items.some(item => item.key.toLowerCase() === key.toLowerCase())) {
      errors.key = '该应用标识已存在';
    }
    if (url && !isValidApplicationUrl(url)) errors.url = '请输入有效的 http 或 https 应用地址（不可包含用户名密码，最长 2048 个字符）';
    setFieldErrors(errors);
    return errors;
  };

  const validateField = (field: FieldName) => {
    const value = (form[field] || '').trim();
    let message = '';
    if (!value && field !== 'url') {
      message = field === 'displayName' ? '请输入应用名称' : '请输入应用标识';
    } else if (field === 'key' && !editingKey && items.some(item => item.key.toLowerCase() === value.toLowerCase())) {
      message = '该应用标识已存在';
    } else if (field === 'url' && value && !isValidApplicationUrl(value)) {
      message = '请输入有效的 http 或 https 应用地址（不可包含用户名密码，最长 2048 个字符）';
    }
    setFieldErrors(current => ({ ...current, [field]: message || undefined }));
  };

  const updateField = <K extends keyof Application>(field: K, value: Application[K]) => {
    setForm(current => ({ ...current, [field]: value }));
    if (field === 'displayName' || field === 'key' || field === 'url') {
      setFieldErrors(current => ({ ...current, [field]: undefined }));
    }
    setSaveError('');
  };

  const closeDrawer = () => {
    if (saving) return;
    setDrawer(false);
    setSaveError('');
    setFieldErrors({});
  };

  const save = async () => {
    if (saving) return;
    if (Object.keys(validate()).length) return;
    setSaving(true);
    setSaveError('');
    try {
      const item = await saveApplication({
        ...form,
        key: form.key.trim(),
        displayName: form.displayName.trim(),
        description: form.description.trim(),
        url: form.url?.trim() || '',
      }, editingKey);
      await load();
      setSelected(item.enabled ? item.key : '');
      setDrawer(false);
      setFieldErrors({});
      MessagePlugin.success(editingKey ? '应用已更新' : '应用已创建');
    } catch (error) {
      setSaveError(getRequestErrorMessage(error, '保存应用失败'));
    } finally {
      setSaving(false);
    }
  };

  const openCreate = () => {
    setForm({ ...empty });
    setEditingKey(undefined);
    setFieldErrors({});
    setSaveError('');
    setDrawer(true);
  };

  const openEdit = (item: Application) => {
    setForm({ ...item });
    setEditingKey(item.key);
    setFieldErrors({});
    setSaveError('');
    setDrawer(true);
  };

  const toggleTheme = () => {
    const next = themeMode === 'light' ? 'dark' : 'light';
    setThemeMode(next);
    applyThemeMode(next);
  };

  return (
    <div className="application-page">
      <header className="application-topbar">
        <strong className="application-brand">权限中心</strong>
        <Button
          className="application-theme-button"
          variant="text"
          shape="circle"
          type="button"
          aria-label={themeMode === 'light' ? '切换到暗色主题' : '切换到浅色主题'}
          icon={themeMode === 'light' ? <MoonIcon size="18px" /> : <SunnyIcon size="18px" />}
          onClick={toggleTheme}
        />
      </header>

      <main className="application-main">
        <div className="application-heading">
          <div>
            <h1>选择业务应用</h1>
            <p>选择应用，管理它的服务资源、菜单和角色权限。</p>
          </div>
          <Button theme="primary" size="large" type="button" icon={<AddIcon />} onClick={openCreate}>新建应用</Button>
        </div>

        <div className="application-toolbar">
          <Input
            className="application-search"
            value={search}
            clearable
            prefixIcon={<SearchIcon />}
            placeholder="搜索应用名称或标识"
            onChange={setSearch}
          />
          <Space className="application-stats" aria-live="polite" size="large">
            <Statistic title="全部" value={items.length} />
            <Statistic title="启用" value={enabledCount} />
          </Space>
        </div>

        {loading ? (
          <Card className="application-state" bordered><div role="status"><Loading size="medium" text="正在加载应用..." /></div></Card>
        ) : loadError ? (
          <Card className="application-state application-state-error" bordered>
            <div role="alert"><Alert theme="error" message={loadError} operation={<Button variant="outline" type="button" onClick={() => void load()}>重试</Button>} /></div>
          </Card>
        ) : !items.length ? (
          <Card className="application-state" bordered><Empty title="暂无业务应用" description="创建应用后即可配置服务、菜单与角色权限。" /></Card>
        ) : !visibleItems.length ? (
          <Card className="application-state" bordered><Empty title="没有匹配的应用" description="请尝试搜索其他名称或标识。" /></Card>
        ) : (
          <div className="application-grid">
            {visibleItems.map(item => (
              <div
                key={item.key}
                className="application-card-hit-area"
                role={item.enabled ? 'button' : 'group'}
                tabIndex={item.enabled ? 0 : -1}
                aria-label={`选择应用：${getApplicationLabel(item)}`}
                onClick={() => { if (item.enabled) setSelected(item.key); }}
                onKeyDown={(event) => {
                  if (item.enabled && (event.key === 'Enter' || event.key === ' ')) {
                    event.preventDefault();
                    setSelected(item.key);
                  }
                }}
              >
                <Card
                  className={`application-card${selected === item.key ? ' is-selected' : ''}${!item.enabled ? ' is-disabled' : ''}`}
                  bordered
                  header={(
                    <Radio
                      className="application-card-radio"
                      checked={selected === item.key}
                      disabled={!item.enabled}
                      value={item.key}
                      onChange={(checked) => { if (checked) setSelected(item.key); }}
                    >
                      <strong title={getApplicationLabel(item)}>{getApplicationLabel(item)}</strong>
                    </Radio>
                  )}
                  footer={(
                    <Space className="application-card-footer" align="center" breakLine>
                      <Tag theme={item.enabled ? 'success' : 'default'} variant="light">{item.enabled ? '已启用' : '已停用'}</Tag>
                      <Space className="application-card-actions" align="center" size="small">
                        {item.enabled && isValidApplicationUrl(item.url) ? (
                          <a
                            className="application-card-link"
                            href={item.url?.trim()}
                            target="_blank"
                            rel="noopener noreferrer"
                            onClick={event => event.stopPropagation()}
                          >
                            打开应用
                          </a>
                        ) : null}
                        <Button variant="text" type="button" icon={<EditIcon />} onClick={(event) => { event.stopPropagation(); openEdit(item); }}>编辑</Button>
                      </Space>
                    </Space>
                  )}
                >
                  <div className="application-card-content">
                    <code>{item.key}</code>
                    <span className="application-description">{item.description || '尚未填写应用描述'}</span>
                  </div>
                </Card>
              </div>
            ))}
          </div>
        )}

        <div className="application-selection-bar">
          <span>{selectedItem ? `已选择：${getApplicationLabel(selectedItem)}` : '请选择一个业务应用'}</span>
          <Button theme="primary" size="large" type="button" disabled={!selectedItem || loading || Boolean(loadError)} onClick={continueToApp} icon={<ArrowRightIcon />}>进入权限配置</Button>
        </div>
      </main>

      <Drawer
        className="application-drawer"
        visible={drawer}
        header={editingKey ? '编辑应用' : '新建应用'}
        size="560px"
        footer={<Space className="application-drawer-actions" size="small">
          <Button type="button" variant="outline" disabled={saving} onClick={closeDrawer}>取消</Button>
          <Button type="submit" form="application-form" theme="primary" loading={saving} disabled={saving}>{editingKey ? '保存修改' : '创建应用'}</Button>
        </Space>}
        closeOnOverlayClick={false}
        closeOnEscKeydown={!saving}
        onCancel={closeDrawer}
        onClose={closeDrawer}
      >
        <Form id="application-form" className="application-form" labelAlign="top" preventSubmitDefault onSubmit={() => void save()}>
          <p className="application-form-intro">先填写应用基本信息，创建后可关联服务资源并配置菜单。</p>
          <Form.FormItem label="应用名称" requiredMark status={fieldErrors.displayName ? 'error' : undefined} help={fieldErrors.displayName || '便于团队识别，可随时修改。'}>
            <><Input
              ref={nameInput}
              autofocus
              value={form.displayName}
              placeholder="例如：业务管理平台"
              disabled={saving}
              onBlur={() => validateField('displayName')}
              onChange={value => updateField('displayName', value)}
            /></>
          </Form.FormItem>
          <Form.FormItem label="应用标识" requiredMark status={fieldErrors.key ? 'error' : undefined} help={fieldErrors.key || '应用的唯一标识，创建后不可修改。'}>
            <><Input
              value={form.key}
              disabled={Boolean(editingKey) || saving}
              placeholder="例如：business.portal"
              onBlur={() => validateField('key')}
              onChange={value => updateField('key', value)}
            /></>
          </Form.FormItem>
          <Form.FormItem label="应用地址" status={fieldErrors.url ? 'error' : undefined} help={fieldErrors.url || '选填，填写后可从应用卡片直接打开。'}>
            <><Input
              value={form.url || ''}
              disabled={saving}
              placeholder="例如：https://portal.example.com"
              onBlur={() => validateField('url')}
              onChange={value => updateField('url', value)}
            /></>
          </Form.FormItem>
          <Form.FormItem label="应用描述">
            <><Textarea
              value={form.description}
              disabled={saving}
              placeholder="简要说明应用用途（选填）"
              onChange={value => updateField('description', value)}
            /></>
          </Form.FormItem>
          <Form.FormItem className="application-switch-item">
            <div className="application-switch-field">
              <div>
                <span>启用应用</span>
                <small>停用后不可进入，仍可编辑和重新启用。</small>
              </div>
              <Switch aria-label="启用应用" value={form.enabled} disabled={saving} onChange={value => updateField('enabled', Boolean(value))} />
            </div>
          </Form.FormItem>
          {saveError ? <Alert className="application-form-error" theme="error" message={saveError} /> : null}
        </Form>
      </Drawer>
    </div>
  );
}
