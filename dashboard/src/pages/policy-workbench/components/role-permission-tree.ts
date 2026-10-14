import type { Menu } from '../../../api/rbac-v1';

export type PermissionNode = Menu & { children: PermissionNode[] };
export const menuTypeLabel = { menu: '菜单', button: '按钮' };

// Keep malformed parent links reachable, without recursing through a cycle or button.
export const buildPermissionTree = (items: Menu[]): PermissionNode[] => {
  const nodes = new Map(items.map(item => [item.id, { ...item, children: [] } as PermissionNode]));
  const roots: PermissionNode[] = [];
  for (const node of nodes.values()) {
    const parent = node.parent_id ? nodes.get(node.parent_id) : undefined;
    const seen = new Set([node.id]);
    let current = parent;
    while (current && !seen.has(current.id)) {
      seen.add(current.id);
      current = current.parent_id ? nodes.get(current.parent_id) : undefined;
    }
    if (!parent || parent.menu_type === 'button' || current) roots.push(node);
    else parent.children.push(node);
  }
  const sort = (values: PermissionNode[]): PermissionNode[] => values
    .sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))
    .map(node => ({ ...node, children: sort(node.children) }));
  return sort(roots);
};

export const permissionPath = (node: Menu, byID: Map<string, Menu>): Menu[] => {
  const result: Menu[] = [];
  const seen = new Set<string>();
  let current: Menu | undefined = node;
  while (current && !seen.has(current.id)) {
    result.unshift(current);
    seen.add(current.id);
    const parent: Menu | undefined = current.parent_id ? byID.get(current.parent_id) : undefined;
    current = parent?.menu_type === 'button' ? undefined : parent;
  }
  return result;
};
export const flattenPermissionTree = (nodes: PermissionNode[]): PermissionNode[] => nodes.flatMap(node => [node, ...flattenPermissionTree(node.children)]);
