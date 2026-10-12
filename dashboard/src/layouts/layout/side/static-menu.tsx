import { useLocation, useNavigate } from 'react-router-dom';
import { Menu } from 'tdesign-react';
import { useState } from 'react';
import { ApiIcon, DashboardIcon, HistoryIcon, LinkIcon, UsergroupIcon, UserIcon, ViewModuleIcon, FilePasteIcon } from 'tdesign-icons-react';
import { setPageLoading } from '../../../page-loading';

const { MenuItem, SubMenu } = Menu;

export const WORKBENCH_NAV = [
  { path: '/dashboard', label: '配置总览', icon: DashboardIcon, group: '' },
  { path: '/service-resources', label: '服务资源', icon: LinkIcon, group: 'resources' },
  { path: '/api-resources', label: 'API 接口', icon: ApiIcon, group: 'resources' },
  { path: '/menus', label: '菜单与按钮', icon: FilePasteIcon, group: 'resources' },
  { path: '/roles', label: '角色管理', icon: UsergroupIcon, group: 'rbac' },
  { path: '/bindings', label: '用户授权', icon: UserIcon, group: 'rbac' },
  { path: '/releases', label: '发布版本', icon: HistoryIcon, group: 'delivery' },
  { path: '/simulate', label: '权限模拟', icon: ViewModuleIcon, group: 'delivery' },
] as const;

export const WORKBENCH_GROUPS = [
  { value: 'resources', label: '资源管理', icon: ApiIcon },
  { value: 'rbac', label: 'RBAC 授权', icon: UsergroupIcon },
  { value: 'delivery', label: '发布与验证', icon: HistoryIcon },
] as const;

const StaticMenu = ({ collapse }: { collapse: boolean }) => {
  const location = useLocation();
  const navigate = useNavigate();
  const currentItem = WORKBENCH_NAV.find(item => location.pathname === item.path);
  const [expanded, setExpanded] = useState<Array<string | number>>(['resources', 'rbac', 'delivery']);
  const [lastPath, setLastPath] = useState(location.pathname);
  if (lastPath !== location.pathname) {
    setLastPath(location.pathname);
    if (currentItem?.group && !expanded.includes(currentItem.group)) setExpanded([...expanded, currentItem.group]);
  }
  return (
    <Menu value={currentItem?.path || '/dashboard'} collapsed={collapse} expanded={expanded} onExpand={setExpanded} onChange={(value) => {
      const path = String(value);
      if (WORKBENCH_NAV.some(item => item.path === path) && path !== location.pathname) { setPageLoading(true); navigate(path); }
    }}>
      <MenuItem value="/dashboard" icon={<DashboardIcon />}>配置总览</MenuItem>
      {WORKBENCH_GROUPS.map(({ value, label, icon: Icon }) => <SubMenu key={value} value={value} title={label} icon={<Icon />}>
        {WORKBENCH_NAV.filter(item => item.group === value).map(({ path, label }) => <MenuItem key={path} value={path}>{label}</MenuItem>)}
      </SubMenu>)}
    </Menu>
  );
};

export default StaticMenu;
