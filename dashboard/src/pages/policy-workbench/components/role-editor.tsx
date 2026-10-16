import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Alert, Button, Checkbox, Dialog, Drawer, Input, Loading, MessagePlugin, Select, Switch, Tag } from 'tdesign-react';
import { ArrowLeftIcon, ChevronDownIcon, ChevronRightIcon, FileIcon, GesturePressIcon, SearchIcon } from 'tdesign-icons-react';
import { useBlocker, useNavigate, useParams } from 'react-router-dom';
import { saveRole, type Role } from '../../../api/rbac-v1';
import { getRequestErrorMessage } from '../../nexus-authorization/shared';
import type { State } from '../shared';
import { setPageLoading } from '../../../page-loading';
import { buildPermissionTree, flattenPermissionTree, menuTypeLabel, permissionPath, type PermissionNode } from './role-permission-tree';
import './role-editor.less';

type Draft = Pick<Role, 'code' | 'name' | 'description' | 'enabled' | 'endpoint_ids' | 'menu_ids'> & { id?: string };
type Undo = { label: string; menuIds?: string[]; endpointIds?: string[] };
const blankDraft = (): Draft => ({ code: '', name: '', description: '', enabled: true, endpoint_ids: [], menu_ids: [] });
const signature = (draft: Draft | null) => draft ? JSON.stringify({ ...draft, menu_ids: [...draft.menu_ids].sort(), endpoint_ids: [...draft.endpoint_ids].sort() }) : '';
const NodeIcon = ({ type }: { type: PermissionNode['menu_type'] }) => type === 'button' ? <GesturePressIcon /> : <FileIcon />;

export default function RoleEditor({ state }: { state: State }) {
  const navigate = useNavigate();
  const { roleId } = useParams();
  const editing = Boolean(roleId);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [original, setOriginal] = useState<Draft | null>(null);
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const allowLeave = useRef(false);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<{ name?: string; code?: string }>({});
  const [tab, setTab] = useState<'menus' | 'apis'>('menus');
  const [menuSearch, setMenuSearch] = useState('');
  const [currentID, setCurrentID] = useState('');
  const [focusID, setFocusID] = useState('');
  const [buttonFocus, setButtonFocus] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [serviceKey, setServiceKey] = useState('');
  const [serviceSearch, setServiceSearch] = useState('');
  const [apiSearch, setApiSearch] = useState('');
  const [moduleFilter, setModuleFilter] = useState('');
  const [methodFilter, setMethodFilter] = useState('');
  const [onlySelected, setOnlySelected] = useState(false);
  const [review, setReview] = useState(false);
  const treeRef = useRef<HTMLDivElement>(null);
  const detailRef = useRef<HTMLDivElement>(null);
  const [undo, setUndo] = useState<Undo | null>(null);
  const role = state.roles.find(item => item.id === roleId);
  const dirty = signature(draft) !== signature(original);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => !allowLeave.current && (dirty || savingRef.current)
    && `${currentLocation.pathname}${currentLocation.search}` !== `${nextLocation.pathname}${nextLocation.search}`);

  useEffect(() => { if (blocker.state === 'blocked') setPageLoading(false); }, [blocker.state]);

  useEffect(() => {
    if (state.loading || state.loadError || draft) return;
    const value: Draft | null = !editing ? blankDraft() : role ? { id: role.id, code: role.code, name: role.name, description: role.description || '', enabled: role.enabled, endpoint_ids: [...(role.endpoint_ids || [])], menu_ids: [...(role.menu_ids || [])] } : null;
    if (value) { setDraft(value); setOriginal(value); }
  }, [draft, editing, role, state.loading, state.loadError]);
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => { if ((dirty || saving) && !allowLeave.current) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  }, [dirty, saving]);

  const activeServices = useMemo(() => new Set(state.services.filter(service => service.isActive).map(service => service.key)), [state.services]);
  const menuByID = useMemo(() => new Map(state.menus.map(menu => [menu.id, menu])), [state.menus]);
  const roots = useMemo(() => buildPermissionTree(state.menus), [state.menus]);
  const allNodes = useMemo(() => flattenPermissionTree(roots), [roots]);
  const nodeByID = useMemo(() => new Map(allNodes.map(node => [node.id, node])), [allNodes]);
  const current = nodeByID.get(currentID);
  const focus = nodeByID.get(focusID);
  const menuIDs = useMemo(() => new Set(draft?.menu_ids || []), [draft?.menu_ids]);
  const endpointIDs = useMemo(() => new Set(draft?.endpoint_ids || []), [draft?.endpoint_ids]);
  const endpointByID = useMemo(() => new Map(state.endpoints.map(item => [item.id, item])), [state.endpoints]);
  const unavailableEndpointIDs = [...endpointIDs].filter(id => !endpointByID.has(id) || !activeServices.has(endpointByID.get(id)!.service_resource));
  const missingMenuIDs = [...menuIDs].filter(id => !menuByID.has(id));
  const blocked = !draft || saving || state.loading || Boolean(state.loadError) || Boolean(unavailableEndpointIDs.length || missingMenuIDs.length);
  const selectedMenus = allNodes.filter(node => menuIDs.has(node.id));
  const menuCount = selectedMenus.filter(node => node.menu_type === 'menu').length;
  const buttonCount = selectedMenus.filter(node => node.menu_type === 'button').length;
  const counts = `${menuCount} 个菜单 · ${buttonCount} 个按钮 · ${endpointIDs.size} 个 API`;

  useEffect(() => {
    if (!allNodes.length) return;
    setExpanded(new Set(allNodes.filter(node => node.children.length).map(node => node.id)));
    setCurrentID(id => nodeByID.has(id) ? id : (allNodes.find(node => node.menu_type === 'menu') || allNodes[0]).id);
  }, [allNodes, nodeByID]);
  useEffect(() => {
    if (tab !== 'menus' || review) return;
    const targetID = buttonFocus || currentID;
    const row = Array.from(treeRef.current?.querySelectorAll<HTMLElement>('[data-menu-node-id]') || []).find(element => element.dataset.menuNodeId === targetID);
    row?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    if (buttonFocus) {
      const card = Array.from(detailRef.current?.querySelectorAll<HTMLElement>('[data-menu-id]') || []).find(element => element.dataset.menuId === buttonFocus);
      card?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
  }, [buttonFocus, currentID, review, tab, focusID]);

  const activeService = state.services.find(service => service.key === serviceKey) || state.services[0];
  const serviceEndpoints = state.endpoints.filter(item => item.service_resource === activeService?.key);
  const modules = [...new Set(serviceEndpoints.map(item => item.module).filter(Boolean))];
  const methods = [...new Set(serviceEndpoints.map(item => item.method))];
  const visibleEndpoints = serviceEndpoints.filter(item => (!apiSearch.trim() || `${item.summary} ${item.path_template} ${item.endpoint_key}`.toLowerCase().includes(apiSearch.trim().toLowerCase()))
    && (!moduleFilter || item.module === moduleFilter) && (!methodFilter || item.method === methodFilter) && (!onlySelected || endpointIDs.has(item.id)));
  const selectableIDs = visibleEndpoints.filter(item => activeService?.isActive && item.enabled).map(item => item.id);
  const selectedResults = visibleEndpoints.filter(item => endpointIDs.has(item.id)).length;
  const selectedSelectable = selectableIDs.filter(id => endpointIDs.has(id)).length;
  const hiddenSelected = serviceEndpoints.filter(item => endpointIDs.has(item.id)).length - selectedResults;
  const ownButtons = current?.menu_type === 'button' ? [current] : current?.children.filter(node => node.menu_type === 'button') || [];
  const childMenus = current?.children.filter(node => node.menu_type !== 'button') || [];

  const patch = useCallback((value: Partial<Draft>) => { setDraft(previous => previous ? { ...previous, ...value } : previous); setError(''); }, []);
  const changeEndpoints = (ids: string[], checked: boolean) => {
    const next = new Set(endpointIDs);
    ids.forEach(id => { if (checked) next.add(id); else next.delete(id); });
    patch({ endpoint_ids: [...next] });
  };
  const changeMenu = (node: PermissionNode, checked: boolean) => {
    const next = new Set(menuIDs);
    if (checked) {
      if (!node.enabled) return;
      // Access switches grant only the node; buttons also grant their valid navigation path.
      const additions = node.menu_type === 'button' ? permissionPath(node, menuByID) : [node];
      additions.filter(item => item.enabled).forEach(item => next.add(item.id));
    } else {
      const removed = flattenPermissionTree([node]).filter(item => next.has(item.id)).map(item => item.id);
      removed.forEach(id => next.delete(id));
      if (removed.length) setUndo({ label: `已移除 ${removed.length} 项权限`, menuIds: removed });
    }
    patch({ menu_ids: [...next] });
  };
  const selectOwnButtons = () => {
    const next = new Set(menuIDs);
    ownButtons.filter(node => node.enabled).forEach(node => permissionPath(node, menuByID).filter(item => item.enabled).forEach(item => next.add(item.id)));
    patch({ menu_ids: [...next] });
  };
  const locateMenu = (node: PermissionNode, fromReview = false) => {
    const target = node.menu_type === 'button' ? nodeByID.get(node.parent_id || '') || node : node;
    if (fromReview) { setReview(false); setTab('menus'); setFocusID(''); setMenuSearch(''); }
    setCurrentID(target.id);
    setButtonFocus(node.menu_type === 'button' ? node.id : '');
    setExpanded(previous => new Set([...previous, ...permissionPath(target, menuByID).map(item => item.id), target.id]));
  };
  const switchService = (key: string) => { setServiceKey(key); setApiSearch(''); setModuleFilter(''); setMethodFilter(''); };
  const restore = () => {
    if (!undo || !draft) return;
    patch({ menu_ids: [...new Set([...draft.menu_ids, ...(undo.menuIds || [])])], endpoint_ids: [...new Set([...draft.endpoint_ids, ...(undo.endpointIds || [])])] });
    setUndo(null);
  };
  const save = async () => {
    if (blocked || !draft || savingRef.current) return;
    const errors = { name: draft.name.trim() ? undefined : '请输入角色名称', code: draft.code.trim() ? undefined : '请输入角色编码' };
    setFieldErrors(errors);
    if (errors.name || errors.code) return;
    savingRef.current = true; setSaving(true); setError('');
    try {
      await saveRole(state.appKey, { ...draft, name: draft.name.trim(), code: draft.code.trim(), description: draft.description.trim() });
      allowLeave.current = true;
      MessagePlugin.success(editing ? '角色已更新' : '角色已创建');
      await state.reload();
      navigate('/roles');
    } catch (cause) { setError(getRequestErrorMessage(cause, '保存角色失败')); }
    finally { savingRef.current = false; setSaving(false); }
  };

  const menuQuery = menuSearch.trim().toLowerCase();
  const matchesMenu = (node: PermissionNode) => `${node.name} ${node.code} ${node.route}`.toLowerCase().includes(menuQuery);
  const hasMenuMatch = (node: PermissionNode): boolean => matchesMenu(node) || node.children.some(hasMenuMatch);
  const renderTree = (nodes: PermissionNode[], depth = 0, ancestorMatch = false): ReactNode => nodes.map(node => {
    const match = !menuQuery || ancestorMatch || matchesMenu(node);
    if (!match && !hasMenuMatch(node)) return null;
    const open = Boolean(menuQuery) || expanded.has(node.id);
    return <div key={node.id} role="none">
      <div role="treeitem" data-menu-node-id={node.id} aria-selected={currentID === node.id || buttonFocus === node.id} aria-expanded={node.children.length ? open : undefined} aria-level={depth + 1} className={`role-tree-row${currentID === node.id || buttonFocus === node.id ? ' is-current' : ''}`} style={{ paddingLeft: 6 + depth * 12 }}>
        {node.children.length ? <button type="button" className="role-tree-chevron" aria-label={`${open ? '收起' : '展开'}${node.name}`} onClick={() => setExpanded(previous => { const next = new Set(previous); if (next.has(node.id)) next.delete(node.id); else next.add(node.id); return next; })}>{open ? <ChevronDownIcon /> : <ChevronRightIcon />}</button> : <span className="role-tree-chevron" />}
        <button type="button" className="role-tree-label" title={`${permissionPath(node, menuByID).map(item => item.name).join(' / ')} · ${menuTypeLabel[node.menu_type]}`} aria-label={`配置${node.name}`} onClick={() => locateMenu(node)}>
          <NodeIcon type={node.menu_type} /><span>{node.name}</span><small className={menuIDs.has(node.id) ? 'is-granted' : ''}>{!node.enabled ? '已停用' : menuIDs.has(node.id) ? node.menu_type === 'button' ? '已选' : '已授权' : node.menu_type === 'button' ? '未选' : '未授权'}</small>
        </button>
      </div>
      {node.children.length && open ? <div role="group">{renderTree(node.children, depth + 1, match)}</div> : null}
    </div>;
  });
  const originalIDs = new Set([...(original?.menu_ids || []).map(id => `m:${id}`), ...(original?.endpoint_ids || []).map(id => `e:${id}`)]);
  const draftIDs = new Set([...menuIDs].map(id => `m:${id}`).concat([...endpointIDs].map(id => `e:${id}`)));
  const added = [...draftIDs].filter(id => !originalIDs.has(id)).length;
  const removed = [...originalIDs].filter(id => !draftIDs.has(id)).length;
  const reviewNodes = selectedMenus.filter(node => node.menu_type !== 'button' || !menuIDs.has(node.parent_id || ''));

  return <div className="rbac-page role-editor">
    <div className="role-editor-topline"><Button tag="button" variant="text" icon={<ArrowLeftIcon />} disabled={saving} onClick={() => navigate('/roles')}>返回角色管理</Button><span>{editing ? '编辑授权' : '新增角色'} · {dirty ? '有未保存的修改' : '尚未修改'}</span></div>
    {state.loadError ? <div role="alert"><Alert theme="error" message={state.loadError} operation={<Button tag="button" variant="text" onClick={() => void state.reload()}>重试</Button>} /></div> : null}
    {!draft && (state.loading || (!editing && !state.loadError)) ? <div className="role-editor-loading" role="status"><Loading text="正在加载角色与权限..." /></div>
      : !draft && !state.loadError && editing ? <section className="role-editor-info"><h2>角色不存在</h2><p>该角色可能已删除，或不属于当前业务应用。</p></section>
        : draft ? <>
          <section className="role-editor-info" aria-label="角色信息">
            <div className="role-editor-fields"><label><span>角色名称 <em>*</em></span><Input aria-label="角色名称" value={draft.name} disabled={saving} status={fieldErrors.name ? 'error' : undefined} placeholder="例如：订单审核员" onChange={name => { patch({ name }); setFieldErrors(previous => ({ ...previous, name: undefined })); }} />{fieldErrors.name ? <small className="role-field-error">{fieldErrors.name}</small> : null}</label>
              <label><span>角色编码 <em>*</em></span><Input aria-label="角色编码" value={draft.code} disabled={editing || saving} status={fieldErrors.code ? 'error' : undefined} placeholder="例如：order-reviewer" onChange={code => { patch({ code }); setFieldErrors(previous => ({ ...previous, code: undefined })); }} />{fieldErrors.code ? <small className="role-field-error">{fieldErrors.code}</small> : null}</label>
              <div className="role-editor-status"><Switch value={draft.enabled} disabled={saving} aria-label="启用角色" onChange={enabled => patch({ enabled: Boolean(enabled) })} /><span>{draft.enabled ? '角色启用' : '角色停用'}</span></div>
            </div>
            <label className="role-editor-description"><span>说明：</span><Input aria-label="角色说明" value={draft.description} disabled={saving} placeholder="角色说明" onChange={description => patch({ description })} /></label>
          </section>
          <fieldset className="role-editor-permissions" disabled={saving || state.loading || Boolean(state.loadError)}>
            <div className="role-editor-tabs" role="tablist" aria-label="授权类型"><button type="button" role="tab" aria-selected={tab === 'menus'} aria-controls="role-menus-panel" id="role-menus-tab" onClick={() => setTab('menus')}>菜单与按钮 · {menuCount + buttonCount}</button><button type="button" role="tab" aria-selected={tab === 'apis'} aria-controls="role-apis-panel" id="role-apis-tab" onClick={() => setTab('apis')}>API 接口 · {endpointIDs.size}</button></div>
            {tab === 'menus' ? <div className="role-editor-split" role="tabpanel" id="role-menus-panel" aria-labelledby="role-menus-tab">
              <aside className="role-editor-tree-panel">
                {focus ? <><div className="role-editor-tree-tools"><button type="button" className="role-text-action" onClick={() => { setFocusID(''); setMenuSearch(''); }}>← 返回完整树</button><button type="button" className="role-text-action" disabled={!nodeByID.has(focus.parent_id || '')} onClick={() => { setFocusID(focus.parent_id || ''); setMenuSearch(''); }}>上一级</button></div><strong className="role-tree-root">当前树根：{focus.name}</strong></> : null}
                <Input value={menuSearch} clearable prefixIcon={<SearchIcon />} placeholder={focus ? '搜索当前分支' : '搜索菜单或按钮'} onChange={setMenuSearch} />
                {!focus ? <div className="role-editor-tree-tools"><span>全部层级</span><button type="button" className="role-text-action" onClick={() => { setExpanded(new Set()); setMenuSearch(''); }}>收起全部</button></div> : null}
                <div ref={treeRef} className="role-editor-tree-scroll" role="tree" aria-label="菜单权限树">{roots.length ? renderTree(focus ? [focus] : roots) : <p className="role-editor-empty">当前应用尚未配置菜单或按钮。</p>}{menuQuery && !(focus ? [focus] : roots).some(hasMenuMatch) ? <p className="role-editor-empty">没有匹配的菜单或按钮</p> : null}</div>
                <div className="role-tree-legend"><span><FileIcon />菜单</span><span><GesturePressIcon />按钮</span></div><p className="role-editor-hint">{focus ? '聚焦只缩小展示范围，不移动节点。完整路径始终可查。' : '层级可继续嵌套；点击箭头展开。按钮为叶节点，点击定位对应操作。'}</p>
              </aside>
              <div ref={detailRef} className="role-editor-detail">
                {current ? <><div className="role-editor-node-heading"><div><h2>{current.name}</h2><p className="role-editor-path">{permissionPath(current, menuByID).map(item => item.name).join(' / ')}</p></div><div className="role-editor-node-actions"><span>{menuIDs.has(current.id) ? '已授权' : '未授权'}</span>{current.menu_type !== 'button' ? <button type="button" className="role-text-action" onClick={() => { setFocusID(current.id); setMenuSearch(''); setExpanded(previous => new Set([...previous, current.id])); }}>聚焦本分支</button> : null}</div></div>
                  {current.menu_type !== 'button' ? <div className="role-editor-access"><div><strong>允许访问此菜单</strong><p>控制菜单和页面可见性，按钮操作在下方单独配置。</p></div><Switch aria-label={`允许访问${current.name}`} value={menuIDs.has(current.id)} disabled={saving || (!current.enabled && !menuIDs.has(current.id))} onChange={checked => changeMenu(current, Boolean(checked))} /></div> : null}
                  {!current.enabled ? <p className="role-editor-hint">此节点已停用，可以移除已有授权。</p> : null}
                  {childMenus.map(child => <button key={child.id} type="button" className="role-editor-child-link" onClick={() => locateMenu(child)}><span>子{menuTypeLabel[child.menu_type]}：{child.name}</span><span>进入配置 →</span></button>)}
                  <div className="role-editor-buttons-heading"><strong>本节点直接按钮 · 已选 {ownButtons.filter(node => menuIDs.has(node.id)).length} / {ownButtons.length}</strong>{ownButtons.length ? <button type="button" className="role-text-action" onClick={selectOwnButtons}>选择本节点全部按钮</button> : null}</div>
                  <div className="role-editor-button-grid">{ownButtons.map(node => <button type="button" role="checkbox" aria-label={`授权${node.name}`} aria-checked={menuIDs.has(node.id)} disabled={saving || (!node.enabled && !menuIDs.has(node.id))} onClick={() => changeMenu(node, !menuIDs.has(node.id))} key={node.id} data-menu-id={node.id} className={`role-editor-button-card${menuIDs.has(node.id) ? ' is-selected' : ''}${buttonFocus === node.id ? ' is-located' : ''}${!node.enabled && !menuIDs.has(node.id) ? ' is-disabled' : ''}`}><span className="role-card-check" aria-hidden="true">{menuIDs.has(node.id) ? '✓' : ''}</span><span><strong>{node.name}</strong><code>{node.code}</code>{!node.enabled ? <small>已停用</small> : null}</span></button>)}</div>
                  {!ownButtons.length ? <p className="role-editor-empty">此节点没有直接按钮，可进入子菜单继续配置。</p> : null}
                  <p className="role-editor-hint">此处只配置当前节点的直接按钮；子菜单可继续进入配置。<br />勾选按钮补齐所有有效祖先，不会授权同级或下级其他按钮。</p>
                </> : <p className="role-editor-empty">在左侧选择节点，配置菜单访问与按钮权限。</p>}
              </div>
            </div> : <div className="role-editor-split role-editor-api-split" role="tabpanel" id="role-apis-panel" aria-labelledby="role-apis-tab">
              <aside className="role-editor-service-panel"><Input value={serviceSearch} clearable prefixIcon={<SearchIcon />} placeholder="搜索服务名称" onChange={setServiceSearch} /><p className="role-editor-hint">关联服务 {state.services.filter(service => state.endpoints.some(item => item.service_resource === service.key && endpointIDs.has(item.id))).length} / {state.services.length} 已授权</p>
                <div className="role-editor-service-list">{state.services.filter(service => `${service.displayName} ${service.key}`.toLowerCase().includes(serviceSearch.trim().toLowerCase())).map(service => { const endpoints = state.endpoints.filter(item => item.service_resource === service.key); return <button key={service.key} type="button" className={`role-editor-service-option${activeService?.key === service.key ? ' is-current' : ''}`} aria-pressed={activeService?.key === service.key} onClick={() => switchService(service.key)}><span><strong>{service.displayName || service.key}</strong><small>{endpoints.filter(item => endpointIDs.has(item.id)).length} / {endpoints.length}</small></span><code>{service.key}</code>{!service.isActive ? <small>已停用</small> : null}</button>; })}</div>
                {!state.services.length ? <p className="role-editor-empty">当前应用尚未关联服务资源。</p> : null}<p className="role-editor-hint">切换服务或筛选条件，保留已有勾选。</p>
              </aside>
              <div className="role-editor-detail">
                {activeService ? <><h2>{activeService.displayName || activeService.key}</h2><p className="role-editor-service-summary">{activeService.key} · {serviceEndpoints.length} 个接口，已授权 {serviceEndpoints.filter(item => endpointIDs.has(item.id)).length} 个</p>
                  {!activeService.isActive ? <p className="role-editor-hint">该服务已停用，可以移除已有选择。</p> : null}
                  <div className="role-editor-api-filters"><Input value={apiSearch} clearable prefixIcon={<SearchIcon />} placeholder="搜索接口名称或路径" onChange={setApiSearch} /><label className="role-filter-select"><span className="role-visually-hidden">模块筛选</span><Select value={moduleFilter} placeholder="模块筛选" options={[{ label: '全部模块', value: '' }, ...modules.map(value => ({ label: value, value }))]} onChange={value => setModuleFilter(String(value))} /></label><label className="role-filter-select"><span className="role-visually-hidden">请求方法</span><Select value={methodFilter} placeholder="请求方法" options={[{ label: '全部方法', value: '' }, ...methods.map(value => ({ label: value, value }))]} onChange={value => setMethodFilter(String(value))} /></label></div>
                  <div className="role-editor-filter-chips"><div>{moduleFilter ? <button type="button" onClick={() => setModuleFilter('')}>{moduleFilter} ×</button> : null}{methodFilter ? <button type="button" onClick={() => setMethodFilter('')}>{methodFilter} ×</button> : null}</div><Checkbox checked={onlySelected} onChange={setOnlySelected}>仅看已选</Checkbox></div>
                  <div className="role-editor-api-bulk"><Checkbox aria-label="全选当前结果" checked={Boolean(selectableIDs.length && selectedSelectable === selectableIDs.length)} indeterminate={selectedSelectable > 0 && selectedSelectable < selectableIDs.length} disabled={saving || !selectableIDs.length} onChange={checked => changeEndpoints(selectableIDs, checked)}>全选当前结果（{selectableIDs.length}）</Checkbox><span>当前已选 {selectedResults} / {visibleEndpoints.length}{hiddenSelected ? ` · 另有 ${hiddenSelected} 个已选被筛选隐藏` : ''}</span></div>
                  <div className="role-editor-api-table-wrap"><table className="role-editor-api-table"><thead><tr><th aria-label="授权选择" /><th>方法</th><th>接口路径</th><th>接口说明</th></tr></thead><tbody>{visibleEndpoints.map(item => { const selected = endpointIDs.has(item.id); const disabled = !selected && (!item.enabled || !activeService.isActive); return <tr key={item.id} data-endpoint-id={item.id} className={`${selected ? 'is-selected' : ''}${disabled ? ' is-disabled' : ''}`} onClick={() => { if (!saving && !disabled) changeEndpoints([item.id], !selected); }}><td onClick={event => event.stopPropagation()}><Checkbox aria-label={`授权${item.method} ${item.path_template}`} checked={selected} disabled={saving || disabled} onChange={checked => changeEndpoints([item.id], checked)} /></td><td><Tag theme={item.method === 'GET' ? 'success' : item.method === 'DELETE' ? 'danger' : 'primary'} variant="light">{item.method}</Tag></td><td><code>{item.path_template}</code></td><td>{item.summary || item.module || '—'}{!item.enabled ? <small className="role-editor-disabled-label">已停用</small> : null}</td></tr>; })}</tbody></table>{!visibleEndpoints.length ? <p className="role-editor-empty">{serviceEndpoints.length ? '没有匹配的接口' : '此服务尚未配置 API'}</p> : null}</div>
                  {modules.length ? <div className="role-editor-module-links"><span>切换模块：</span>{modules.filter(value => value !== moduleFilter).map(value => <button type="button" key={value} onClick={() => setModuleFilter(value)}>{value}（{serviceEndpoints.filter(item => item.module === value).length}）</button>)}</div> : null}<p className="role-editor-hint">批量勾选只影响当前筛选结果，不会改变其他模块或服务的授权。</p>
                </> : <p className="role-editor-empty">关联服务后即可配置 API 权限。</p>}
              </div>
            </div>}
          </fieldset>
          {unavailableEndpointIDs.length || missingMenuIDs.length ? <div role="alert"><Alert theme="warning" message="部分原授权已删除，或所属服务已停用 / 解除关联。移除不可用授权后才能保存。" operation={<Button tag="button" variant="text" disabled={saving} onClick={() => { setUndo(null); patch({ endpoint_ids: draft.endpoint_ids.filter(id => !unavailableEndpointIDs.includes(id)), menu_ids: draft.menu_ids.filter(id => !missingMenuIDs.includes(id)) }); }}>移除不可用授权</Button>} /></div> : null}
          {error ? <div role="alert"><Alert theme="error" message={error} /></div> : null}
          <footer className="role-editor-footer"><div><span>{counts}</span><Button tag="button" variant="text" theme="primary" disabled={saving} onClick={() => setReview(true)}>查看已选权限</Button></div><div><Button tag="button" variant="outline" disabled={saving} onClick={() => navigate('/roles')}>取消</Button><Button tag="button" theme="primary" loading={saving} disabled={blocked} onClick={() => void save()}>保存角色</Button></div></footer>
          <p className="role-editor-save-hint">保存后角色授权立即生效。</p>
          <Drawer className="rbac-drawer role-permission-review" header="已选权限" visible={review} size="560px" closeBtn={!saving} onClose={() => { if (!saving) setReview(false); }} footer={<div className="role-review-footer"><span>关闭抽屉会保留当前选择。</span><Button tag="button" theme="primary" disabled={saving} onClick={() => setReview(false)}>继续配置</Button></div>}>
            <p className="role-review-summary">{draft.name.trim() || '未命名角色'} · {counts}</p><div className="role-review-changes">相较上次保存：新增 {added} 项 · 移除 {removed} 项</div>
            <div className="role-review-section-heading"><h3>菜单与按钮</h3><button type="button" className="role-text-action" disabled={saving} onClick={() => { setReview(false); setTab('menus'); }}>返回配置</button></div>
            {reviewNodes.map(node => { const buttons = node.children.filter(child => child.menu_type === 'button' && menuIDs.has(child.id)); return <div key={node.id} className="role-review-menu"><div><strong>{node.name}</strong><p>{permissionPath(node, menuByID).map(item => item.name).join(' / ')}</p><p>{node.menu_type === 'button' ? '按钮权限' : '菜单访问'}{buttons.length ? ` · ${buttons.map(child => child.name).join('、')}` : ''}</p></div><button type="button" className="role-text-action" disabled={saving} onClick={() => locateMenu(node, true)}>定位</button></div>; })}
            {!reviewNodes.length ? <p className="role-editor-empty">尚未选择菜单或按钮。</p> : null}
            <div className="role-review-section-heading"><h3>API 接口</h3><button type="button" className="role-text-action" disabled={saving} onClick={() => { setReview(false); setTab('apis'); }}>返回配置</button></div>
            {[...new Set(state.endpoints.filter(item => endpointIDs.has(item.id)).map(item => item.service_resource))].map(key => { const selected = state.endpoints.filter(item => item.service_resource === key && endpointIDs.has(item.id)); return <div key={key} className="role-review-api-group"><button type="button" className="role-review-service" disabled={saving} onClick={() => { switchService(key); setTab('apis'); setReview(false); }}>{state.services.find(service => service.key === key)?.displayName || key} · {selected.length}</button>{selected.map(item => <div className="role-review-api" key={item.id}><code>{item.method} {item.path_template}</code><button type="button" aria-label={`移除${item.method} ${item.path_template}`} disabled={saving} onClick={() => { changeEndpoints([item.id], false); setUndo({ label: '已移除 1 个 API', endpointIds: [item.id] }); }}>×</button></div>)}</div>; })}
            {!endpointIDs.size ? <p className="role-editor-empty">尚未选择 API 接口。</p> : null}
            {missingMenuIDs.length || unavailableEndpointIDs.length ? <p className="role-field-error">存在不可用授权，请返回配置移除后再保存。</p> : null}
          </Drawer>
        </> : null}
    {undo ? <div className="role-editor-undo" role="status"><span>{undo.label}</span><button type="button" disabled={saving} onClick={restore}>撤销</button><button type="button" aria-label="关闭撤销提示" onClick={() => setUndo(null)}>×</button></div> : null}
    <Dialog header={saving ? '正在保存角色' : '还有未保存的修改'} visible={blocker.state === 'blocked'} onClose={() => blocker.state === 'blocked' && blocker.reset()} footer={<><Button tag="button" variant="outline" disabled={saving} onClick={() => blocker.state === 'blocked' && blocker.proceed()}>放弃修改并离开</Button><Button tag="button" theme="primary" onClick={() => blocker.state === 'blocked' && blocker.reset()}>继续编辑</Button></>}>{saving ? '保存完成后可以离开，请稍候。' : '离开后本次角色信息与授权修改将丢失。你可以继续编辑，或放弃修改并离开。'}</Dialog>
  </div>;
}
