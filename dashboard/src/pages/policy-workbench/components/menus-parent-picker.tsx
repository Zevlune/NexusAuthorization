import { useMemo, useState } from 'react';
import { Input, Space, TreeSelect, Typography } from 'tdesign-react';
import type { TreeNodeModel } from 'tdesign-react';
import { AppIcon, CheckIcon, GesturePressIcon, RootListIcon, SearchIcon } from 'tdesign-icons-react';
import type { Menu } from '../../../api/rbac-v1';
import './menus-parent-picker.less';

export type ParentPickerType = Menu['menu_type'] | 'root';

export type ParentPickerOption = {
  id: string;
  name: string;
  code: string;
  type: ParentPickerType;
  /** Full ancestor path, e.g. 工作空间 / 业务中心 / 订单中心. */
  path: string;
  depth: number;
  disabled?: boolean;
  disabledReason?: string;
};

type Props = {
  value: string | null;
  options: ParentPickerOption[];
  placeholder: string;
  note: string;
  disabled?: boolean;
  onChange: (id: string | null) => void;
};

// 根选项的空 id 需要映射为一个独特值，否则会被 TreeSelect 当作“未选中”而显示占位符。
const ROOT_VALUE = '__menus_parent_root__';

type ParentTreeNode = {
  value: string;
  /** 完整路径，用于选中后的回显文案。 */
  label: string;
  /** 悬浮提示 / 默认搜索文本。 */
  text: string;
  name: string;
  code: string;
  type: ParentPickerType;
  disabled?: boolean;
  disabledReason?: string;
  children?: ParentTreeNode[];
};

const OptionIcon = ({ type, className }: { type: ParentPickerType; className?: string }) => {
  if (type === 'root') return <RootListIcon className={className} />;
  if (type === 'button') return <GesturePressIcon className={className} />;
  return <AppIcon className={className} />;
};

const toNode = (option: ParentPickerOption): ParentTreeNode => ({
  value: option.id === '' ? ROOT_VALUE : option.id,
  label: option.path,
  text: option.path,
  name: option.name,
  code: option.code,
  type: option.type,
  disabled: option.disabled,
  disabledReason: option.disabledReason,
});

/**
 * ParentPickerOption 是带 depth 的先序平铺列表，按 depth 递归还原真实的 children 树。
 * 父节点一定先于其子节点出现，因此用一个 depth 栈即可确定父子关系。
 */
const buildTree = (options: ParentPickerOption[]): ParentTreeNode[] => {
  const roots: ParentTreeNode[] = [];
  const stack: ParentTreeNode[] = [];
  options.forEach((option) => {
    const node = toNode(option);
    const depth = Math.max(0, option.depth);
    stack.length = Math.min(stack.length, depth);
    const parent = depth > 0 ? stack[depth - 1] : undefined;
    if (parent) {
      (parent.children || (parent.children = [])).push(node);
    } else {
      roots.push(node);
    }
    stack[depth] = node;
  });
  return roots;
};

export default function MenusParentPicker({ value, options, placeholder, note, disabled, onChange }: Props) {
  const [query, setQuery] = useState('');
  const treeData = useMemo(() => buildTree(options), [options]);
  const hasRoot = useMemo(() => options.some((option) => option.id === ''), [options]);

  const selectedValue = value == null || value === '' ? (hasRoot ? ROOT_VALUE : undefined) : value;

  // treeProps.filter 的入参是 Tree 的 TreeNodeModel，节点数据在 node.data 上。
  // 搜索词为空时传 undefined，让 Tree 关闭 hasFilter，保留正常的展开/折叠能力。
  const treeFilter = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return undefined;
    return (node: TreeNodeModel) => {
      const data = node.data as unknown as ParentTreeNode;
      // label 就是完整 path，因此这里覆盖“全路径 + name + code”的搜索。
      return `${data.name} ${data.code} ${data.label}`.toLowerCase().includes(keyword);
    };
  }, [query]);

  const renderNode = (node: TreeNodeModel) => {
    const data = node.data as unknown as ParentTreeNode;
    const selected = data.value === selectedValue;
    return <Space
      className={`menus-parent-picker-node${selected ? ' is-selected' : ''}${data.disabled ? ' is-disabled' : ''}`}
      size={8}
      align="center"
    >
      <OptionIcon type={data.type} className="menus-parent-picker-node-icon" />
      <Typography.Text className="menus-parent-picker-node-name">{data.name}</Typography.Text>
      <Space className="menus-parent-picker-node-meta" size={6} align="center">
        {data.disabled && data.disabledReason
          ? <Typography.Text theme="secondary" className="menus-parent-picker-node-reason">{data.disabledReason}</Typography.Text>
          : null}
        {selected ? <CheckIcon className="menus-parent-picker-node-check" /> : null}
      </Space>
    </Space>;
  };

  return <TreeSelect
    className="menus-parent-picker"
    data={treeData}
    value={selectedValue}
    placeholder={placeholder}
    disabled={disabled}
    clearable={false}
    empty={<Typography.Text className="menus-parent-picker-empty" theme="secondary">没有匹配的节点</Typography.Text>}
    panelTopContent={<Space className="menus-parent-picker-search" size={0}>
      <Input value={query} clearable prefixIcon={<SearchIcon />} placeholder="搜索节点名称或完整路径" onChange={setQuery} />
    </Space>}
    panelBottomContent={<Typography.Text className="menus-parent-picker-note" theme="secondary">{note}</Typography.Text>}
    popupProps={{
      placement: 'bottom-left',
      showArrow: false,
      overlayInnerClassName: 'menus-parent-picker-overlay',
      overlayInnerStyle: {
        width: 504,
        maxWidth: 'calc(100vw - 32px)',
        maxHeight: 'calc(100vh - 96px)',
        padding: 0,
        overflow: 'hidden',
        border: '1px solid var(--app-border)',
        borderRadius: 6,
        background: 'var(--app-surface)',
        boxShadow: '0 12px 32px rgba(15, 23, 42, 0.12)',
      },
    }}
    treeProps={{
      className: 'menus-parent-picker-tree',
      // 关闭父子禁用联动，保证 disabled 父节点下可选的子节点依然可以展开并选中。
      checkStrictly: true,
      expandAll: true,
      label: renderNode,
      filter: treeFilter,
    }}
    onPopupVisibleChange={(visible) => {
      if (!visible) setQuery('');
    }}
    onChange={(next) => {
      if (next == null || next === '' || next === ROOT_VALUE) {
        onChange(null);
        return;
      }
      onChange(String(next));
    }}
  />;
}
