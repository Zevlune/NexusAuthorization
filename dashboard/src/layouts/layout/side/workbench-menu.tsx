import { useEffect, useMemo, useState, type ComponentType } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Menu } from 'tdesign-react';
import { ApiIcon, DashboardIcon, FilePasteIcon, HistoryIcon, LinkIcon, UsergroupIcon, ViewModuleIcon, type IconProps } from 'tdesign-icons-react';
import { getWorkbenchNavItem, useWorkbenchNavigation } from '../workbench-navigation';
import { visibleWorkbenchMenus, workbenchMenuTrail, type WorkbenchMenu } from '../../../router/workbench-menu';
import { setPageLoading } from '../../../page-loading';

const { MenuItem, SubMenu } = Menu;
const icons: Record<string, ComponentType<IconProps>> = {
  api: ApiIcon, dashboard: DashboardIcon, menus: FilePasteIcon, history: HistoryIcon,
  link: LinkIcon, usergroup: UsergroupIcon, simulate: ViewModuleIcon,
};

const WorkbenchMenuComponent = ({ collapse }: { collapse: boolean }) => {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const menus = useWorkbenchNavigation();
  const tree = useMemo(() => visibleWorkbenchMenus(menus), [menus]);
  const currentItem = getWorkbenchNavItem(menus, pathname);
  const [expanded, setExpanded] = useState<Array<string | number>>(() => (
    tree.filter(menu => menu.children.length).map(menu => menu.path)
  ));

  useEffect(() => {
    const parents = workbenchMenuTrail(menus, pathname).filter(menu => menu.show && menu.children.length).map(menu => menu.path);
    setExpanded(previous => [...new Set([...previous, ...parents])]);
  }, [menus, pathname]);

  const renderMenu = (menu: WorkbenchMenu) => {
    const Icon = menu.iconName ? icons[menu.iconName.toLowerCase()] : undefined;
    const icon = Icon ? <Icon /> : undefined;
    return menu.children.length
      ? <SubMenu key={menu.id} value={menu.path} title={menu.name} icon={icon} popupProps={collapse ? { trigger: 'hover', attach: () => document.body } : undefined}>{menu.children.map(renderMenu)}</SubMenu>
      : <MenuItem key={menu.id} value={menu.path} icon={icon} disabled={!menu.componentPath}>{menu.name}</MenuItem>;
  };

  return <Menu value={currentItem?.path || ''} collapsed={collapse} expanded={expanded} onExpand={setExpanded} onChange={value => {
    const path = String(value);
    const item = getWorkbenchNavItem(tree, path);
    if (item?.path === path && path !== pathname) { setPageLoading(true); navigate(path); }
  }}>{tree.map(renderMenu)}</Menu>;
};

export default WorkbenchMenuComponent;
