import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Card, Descriptions, Form, Input, Select, Tag } from 'tdesign-react';
import { simulateRBAC, type Endpoint } from '../../../api/rbac-v1';
import { listAllUsers, scimUserLabel, type SCIMUser } from '../../../api/user';
import { getRequestErrorMessage } from '../../nexus-authorization/shared';
import { Empty, fail, Guard, Page, type State } from '../shared';

// OpenAPI path parameters look like {id}; each match captures the parameter name.
const paramMatcher = () => /\{([^}/]+)\}/g;

export const pathParamNames = (template: string): string[] => {
  const names: string[] = [];
  for (const match of template.matchAll(paramMatcher())) {
    const name = match[1];
    if (!names.includes(name)) names.push(name);
  }
  return names;
};

// Parameter names are arbitrary, so `constructor`, `toString` or `__proto__` are legal. A Map never
// falls back to Object.prototype, so an unfilled parameter reads back as '' instead of a method.
export type PathParams = Map<string, string>;

const readParam = (values: PathParams, name: string): string => {
  const value = values.get(name);
  return typeof value === 'string' ? value : '';
};

// Replace every placeholder with its single-segment encoded value. Unfilled values stay as the
// literal placeholder so the preview shows what is missing; the run button stays disabled until
// every parameter is supplied, so a template is never submitted as a real request path.
export const resolvePathTemplate = (template: string, values: PathParams): string =>
  template.replace(paramMatcher(), (literal, name: string) => {
    const value = readParam(values, name).trim();
    return value ? encodeURIComponent(value) : literal;
  });

type PathOption = { template: string; summary: string; methods: string[] };

const buildPathOptions = (endpoints: Endpoint[], service: string): PathOption[] => {
  const options = new Map<string, PathOption>();
  for (const endpoint of endpoints) {
    if (endpoint.service_resource !== service) continue;
    const template = endpoint.path_template || '';
    if (!template) continue;
    const existing = options.get(template);
    if (!existing) {
      options.set(template, { template, summary: endpoint.summary || '', methods: endpoint.method ? [endpoint.method] : [] });
      continue;
    }
    if (endpoint.method && !existing.methods.includes(endpoint.method)) existing.methods.push(endpoint.method);
    if (!existing.summary && endpoint.summary) existing.summary = endpoint.summary;
  }
  return [...options.values()];
};

const pathOptionLabel = (option: PathOption) => option.summary ? `${option.template} · ${option.summary}` : option.template;

export default function Simulate({ state }: { state: State }) {
  const activeServices = useMemo(() => state.services.filter(item => item.isActive), [state.services]);
  // The shared catalog is only trustworthy once the current application finished loading: while a
  // reload is pending it still holds the previous application's services, and after a failed load
  // it may hold stale entries. Simulate remounts per application, so nothing is reconciled until
  // the store reports a loaded catalog.
  const catalogReady = !state.loading && !state.loadError;
  const [service, setService] = useState('');
  const [path, setPath] = useState('');
  const [method, setMethod] = useState('');
  const [subject, setSubject] = useState('');
  const [params, setParams] = useState<PathParams>(() => new Map());
  const [result, setResult] = useState<unknown>(null);
  const [running, setRunning] = useState(false);
  const [users, setUsers] = useState<SCIMUser[]>([]);
  const [usersLoading, setUsersLoading] = useState(true);
  const [usersError, setUsersError] = useState('');
  const [retry, setRetry] = useState(0);

  // Only one simulate request is in flight at a time. Bumping the token discards a late response
  // whose form context already changed, so a stale allow is never written back into the form.
  const runToken = useRef(0);
  const resetResult = useCallback(() => { runToken.current += 1; setResult(null); }, []);

  const pathOptions = useMemo(() => buildPathOptions(state.endpoints, service), [state.endpoints, service]);

  useEffect(() => {
    if (!catalogReady) return;
    // The selected service can leave the catalog: removed, deactivated, or no longer associated
    // with the application. `activeServices` is then empty while the shared endpoint directory may
    // still list that service's paths, so the service is re-defaulted and every dependent selection
    // plus the previous verdict is cleared, keeping a stale allow from pointing at a missing resource.
    if (!activeServices.some(item => item.key === service)) {
      // Nothing is selected and no service exists: return without resetting so an empty catalog
      // cannot loop this effect through `resetResult`.
      if (!service && !activeServices.length) return;
      setService(activeServices[0]?.key || '');
      setPath('');
      setMethod('');
      setParams(new Map());
      resetResult();
      return;
    }
    // A catalog refresh can also drop the selected path or one of its methods while its service stays.
    if (!path) return;
    const option = pathOptions.find(item => item.template === path);
    if (!option) {
      setPath('');
      setMethod('');
      setParams(new Map());
      resetResult();
      return;
    }
    if (method && !option.methods.includes(method)) {
      setMethod('');
      resetResult();
    }
  }, [catalogReady, activeServices, pathOptions, service, path, method, resetResult]);

  useEffect(() => {
    const controller = new AbortController();
    setUsersLoading(true);
    setUsersError('');
    void listAllUsers(controller.signal).then(values => {
      if (!controller.signal.aborted) setUsers(values);
    }).catch(error => {
      if (!controller.signal.aborted) {
        setUsers([]);
        setUsersError(getRequestErrorMessage(error, '加载用户目录失败'));
      }
    }).finally(() => {
      if (!controller.signal.aborted) setUsersLoading(false);
    });
    return () => controller.abort();
  }, [retry]);

  const selectedPath = pathOptions.find(option => option.template === path);
  const methods = selectedPath?.methods ?? [];
  const paramNames = selectedPath ? pathParamNames(selectedPath.template) : [];
  const finalPath = selectedPath ? resolvePathTemplate(selectedPath.template, params) : '';
  const selectedUser = users.find(user => user.id === subject);
  const directoryEmpty = !usersLoading && !usersError && users.length === 0;
  const noActiveUsers = !usersLoading && !usersError && users.length > 0 && users.every(user => user.active === false);
  const paramsComplete = paramNames.every(name => readParam(params, name).trim() !== '');
  // The service must still be active and the method must still belong to the chosen path, and
  // nothing runs while the shared store is loading or failed, so a stale catalog entry can never be
  // submitted as a real request.
  const serviceIsActive = activeServices.some(item => item.key === service);
  const canRun = Boolean(state.appKey && catalogReady && serviceIsActive
    && selectedPath && methods.includes(method) && selectedUser
    && selectedUser.active !== false && paramsComplete && !usersLoading && !usersError);

  const run = async () => {
    if (running || !canRun) return;
    const token = ++runToken.current;
    setRunning(true);
    // Clear the previous outcome up front so a failed or in-flight request never shows a stale allow.
    setResult(null);
    try {
      // The default domain is omitted; the backend normalizes a missing/blank domain to ''.
      const next = await simulateRBAC(state.appKey, { service_resource: service, subject, method, path: finalPath });
      if (token === runToken.current) setResult(next);
    } catch (error) {
      if (token === runToken.current) fail(error, '权限测试失败');
    } finally {
      setRunning(false);
    }
  };

  const changeService = (value: string) => { setService(value); setPath(''); setMethod(''); setParams(new Map()); resetResult(); };
  const changePath = (value: string) => {
    const option = pathOptions.find(item => item.template === value);
    setPath(value);
    setParams(new Map());
    setMethod(current => option && option.methods.includes(current) ? current : '');
    resetResult();
  };
  const changeMethod = (value: string) => { setMethod(value); resetResult(); };
  const changeSubject = (value: string) => { setSubject(value); resetResult(); };
  const changeParam = (name: string, value: string) => { setParams(current => new Map(current).set(name, value)); resetResult(); };

  return <Guard state={state}><Page title="权限模拟" actions={<Button tag="button" className="simulate-run" theme="primary" loading={running} disabled={!canRun || running} onClick={() => void run()}>运行测试</Button>}>
    <Card className="rbac-card">
      {usersError ? <div role="alert" className="simulate-directory-alert"><Alert theme="error" message={usersError} operation={<Button variant="text" disabled={running} onClick={() => setRetry(value => value + 1)}>重试</Button>} /></div> : null}
      {directoryEmpty ? <div role="alert" className="simulate-directory-alert"><Alert theme="warning" message="用户目录为空，暂无可用模拟用户。" operation={<Button variant="text" disabled={running} onClick={() => setRetry(value => value + 1)}>重试</Button>} /></div> : null}
      {noActiveUsers ? <div role="alert" className="simulate-directory-alert"><Alert theme="warning" message="用户目录中没有启用的用户，无法进行权限模拟。" operation={<Button variant="text" disabled={running} onClick={() => setRetry(value => value + 1)}>重试</Button>} /></div> : null}
      <Form className="simulate-form" labelAlign="top" colon={false} onSubmit={() => void run()}>
        <div className="simulate-grid" data-testid="simulate-grid">
          <div className="simulate-field" data-field="service">
            <Form.FormItem label="目标服务"><><Select className="simulate-control" value={service} placeholder="请选择目标服务" disabled={running} options={activeServices.map(item => ({ label: `${item.displayName || item.key} · ${item.key}`, value: item.key }))} onChange={value => changeService(String(value))} /></></Form.FormItem>
          </div>
          <div className="simulate-field" data-field="path">
            <Form.FormItem label="Path"><><Select className="simulate-control" filterable value={path} placeholder="请选择接口路径" disabled={running || !service} options={pathOptions.map(item => ({ label: pathOptionLabel(item), value: item.template }))} empty={service ? '当前服务没有可用接口' : '请先选择目标服务'} onChange={value => changePath(String(value))} /></></Form.FormItem>
          </div>
          <div className="simulate-field" data-field="method">
            <Form.FormItem label="HTTP 方法"><><Select className="simulate-control" value={method} placeholder="请选择HTTP方法" disabled={running || !selectedPath} options={methods.map(item => ({ label: item, value: item }))} empty="请先选择接口路径" onChange={value => changeMethod(String(value))} /></></Form.FormItem>
          </div>
          <div className="simulate-field" data-field="user">
            <Form.FormItem label="用户"><><Select className="simulate-control" filterable value={subject} placeholder="请选择用户" loading={usersLoading} loadingText="正在加载用户..." disabled={running || usersLoading || Boolean(usersError)} options={users.map(user => ({ label: `${scimUserLabel(user)}${user.active === false ? ' · 已停用' : ''}`, value: user.id, disabled: user.active === false }))} empty={directoryEmpty ? '目录中没有用户' : '没有匹配的用户'} onChange={value => changeSubject(String(value))} /></></Form.FormItem>
          </div>
          {selectedPath ? <div className="simulate-field simulate-params" data-field="params">
            {paramNames.length ? <Form.FormItem label="路径参数"><div className="simulate-param-list">{paramNames.map(name => <Input key={name} className="simulate-param-input" disabled={running} value={readParam(params, name)} placeholder={`请输入${name}`} onChange={value => changeParam(name, value)} />)}</div></Form.FormItem> : null}
            <div className="simulate-final-path" data-testid="simulate-final-path"><span>最终请求路径</span><code>{finalPath}</code></div>
          </div> : null}
        </div>
      </Form>
    </Card>
    <Card className="rbac-card" title="测试结果">{result && typeof result === 'object' ? <Descriptions column={1} bordered items={Object.entries(result).map(([label, value]) => ({ label, content: typeof value === 'boolean' ? <Tag theme={value ? 'success' : 'danger'}>{String(value)}</Tag> : Array.isArray(value) ? value.join('、') || '-' : String(value ?? '-') }))} /> : <Empty text="选择目标服务、接口路径、HTTP 方法与用户后运行测试" />}</Card>
  </Page></Guard>;
}
