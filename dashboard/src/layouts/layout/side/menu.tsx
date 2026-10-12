import { type ComponentType, useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Menu, MenuValue } from 'tdesign-react';
import {
  ApiIcon,
  CloudIcon,
  DashboardIcon,
  FolderIcon,
  LockOnIcon,
  MenuApplicationIcon,
  ToolsIcon,
  UserIcon,
  UsergroupIcon,
} from 'tdesign-icons-react';
import type { IconProps } from 'tdesign-icons-react';

import { setPageLoading } from '../../../page-loading';
import type { NavigationMenu } from '../../../router/dynamic-routes';

const { MenuItem, SubMenu } = Menu;

interface IProp {
  collapse: boolean;
  menus: NavigationMenu[];
}

// 后端返回图标标识，前端只负责将标识解析为已打包的 TDesign 图标组件。
const menuIconComponents: Record<string, ComponentType<IconProps>> = {
  api: ApiIcon,
  cloud: CloudIcon,
  dashboard: DashboardIcon,
  folder: FolderIcon,
  lock: LockOnIcon,
  'lock-on': LockOnIcon,
  menu: MenuApplicationIcon,
  'menu-application': MenuApplicationIcon,
  tools: ToolsIcon,
  user: UserIcon,
  usergroup: UsergroupIcon,
};

const getMenuIcon = (iconName: string) => {
  const IconComponent = menuIconComponents[iconName.trim().toLowerCase()] || FolderIcon;
  return <IconComponent />;
};

const findMenu = (menus: NavigationMenu[], path: string): NavigationMenu | undefined => {
  for (const menu of menus) {
    if (menu.path === path) {
      return menu;
    }
    const child = findMenu(menu.children, path);
    if (child) {
      return child;
    }
  }
  return undefined;
};

const activeParentPaths = (menus: NavigationMenu[], pathname: string): string[] => {
  const expanded: string[] = [];
  const visit = (nodes: NavigationMenu[]) => {
    for (const node of nodes) {
      if (node.children.length > 0) {
        const childMatches = node.children.some((child) => (
          child.path === pathname || pathname.startsWith(`${child.path}/`)
        ));
        if (childMatches || pathname === node.path || pathname.startsWith(`${node.path}/`)) {
          expanded.push(node.path);
        }
        visit(node.children);
      }
    }
  };
  visit(menus);
  return expanded;
};

const MenuComponent = ({ collapse, menus }: IProp) => {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState<string[]>([]);

  useEffect(() => {
    setExpanded(activeParentPaths(menus, pathname));
  }, [menus, pathname]);

  const menuPaths = useMemo(() => {
    const paths = new Set<string>();
    const collect = (nodes: NavigationMenu[]) => {
      nodes.forEach((node) => {
        paths.add(node.path);
        collect(node.children);
      });
    };
    collect(menus);
    return paths;
  }, [menus]);

  const navigateWithLoading = (nextPath: string) => {
    if (!nextPath || nextPath === pathname) {
      return;
    }

    setPageLoading(true);
    navigate(nextPath);
  };

  const onMenuChange = (value: MenuValue) => {
    const nextPath = String(value);
    if (!menuPaths.has(nextPath)) {
      return;
    }

    const selectedMenu = findMenu(menus, nextPath);
    if (selectedMenu?.children.length) {
      return;
    }

    navigateWithLoading(nextPath);
  };

  const renderMenu = (menu: NavigationMenu) => {
    const icon = getMenuIcon(menu.icon);
    if (menu.children.length > 0) {
      return (
        <SubMenu key={menu.id || menu.path} value={menu.path} title={menu.name} icon={icon}>
          {menu.children.map(renderMenu)}
        </SubMenu>
      );
    }

    return (
      <MenuItem key={menu.id || menu.path} value={menu.path} icon={icon}>
        {menu.name}
      </MenuItem>
    );
  };

  return (
    <Menu
      value={pathname}
      collapsed={collapse}
      expanded={expanded}
      onChange={onMenuChange}
      onExpand={(values) => setExpanded(values.map(String))}
    >
      {menus.map(renderMenu)}
    </Menu>
  );
};

export default MenuComponent;
