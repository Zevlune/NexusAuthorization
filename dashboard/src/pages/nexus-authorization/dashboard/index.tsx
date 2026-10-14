import { useCallback, useEffect, useState } from 'react';
import { Button, Card, Col, Descriptions, MessagePlugin, Row, Space, Statistic, Steps, Tag } from 'tdesign-react';

import { getMenuTree, listRoles, type Menu, type Role } from '../../../api/permission';
import { getRequestErrorMessage, PageHeader, useServiceResourceScope } from '../shared';
import '../style.less';

type MenuCounts = {
  menus: number;
  buttons: number;
};

const countMenuTree = (nodes: Menu[]): MenuCounts => {
  return nodes.reduce<MenuCounts>((total, node) => {
    const own = node.type === 'button'
      ? { menus: 0, buttons: 1 }
      : { menus: 1, buttons: 0 };
    const children = countMenuTree(node.children);
    return {
      menus: total.menus + own.menus + children.menus,
      buttons: total.buttons + own.buttons + children.buttons,
    };
  }, { menus: 0, buttons: 0 });
};

const DashboardPage = () => {
  const { serviceResource } = useServiceResourceScope();
  const [roles, setRoles] = useState<Role[]>([]);
  const [menuCounts, setMenuCounts] = useState<MenuCounts>({ menus: 0, buttons: 0 });
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);

  const loadSummary = useCallback(async (scope: string) => {
    setLoading(true);
    setLoaded(false);
    try {
      const [nextRoles, nextTree] = await Promise.all([listRoles(scope), getMenuTree(scope)]);
      setRoles(nextRoles);
      setMenuCounts(countMenuTree(nextTree));
      setLoaded(true);
    } catch (error) {
      setRoles([]);
      setMenuCounts({ menus: 0, buttons: 0 });
      MessagePlugin.error(getRequestErrorMessage(error, '加载权限中心摘要失败'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadSummary(serviceResource);
  }, [serviceResource, loadSummary]);

  const enabledRoleCount = roles.filter((role) => role.enabled).length;
  const totalPermissionCount = menuCounts.menus + menuCounts.buttons;

  return (
    <div className="permission-page permission-dashboard-page">
      <PageHeader
        title="仪表盘"
        actions={<Tag theme="primary" variant="light-outline">RBAC</Tag>}
      />

      <div className="permission-toolbar">
        <span className="permission-toolbar-meta">当前服务资源：{serviceResource}</span>
      </div>

      <Row gutter={[16, 16]}>
        <Col xs={12} md={6}>
          <Card className="permission-dashboard-metric" bordered>
            <Descriptions column={1} items={[{ label: '服务资源标识', content: <code className="permission-table-code" title={serviceResource}>{serviceResource}</code> }]} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card className="permission-dashboard-metric" bordered>
            <Statistic title="启用角色" value={enabledRoleCount} loading={loading} extra={`${roles.length} 个角色记录`} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card className="permission-dashboard-metric" bordered>
            <Statistic title="菜单节点" value={menuCounts.menus} loading={loading} extra="用于构建导航树" />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card className="permission-dashboard-metric" bordered>
            <Statistic title="按钮权限" value={menuCounts.buttons} loading={loading} extra="用于控制操作入口" />
          </Card>
        </Col>
      </Row>

      <Card
        className="permission-card permission-dashboard-status"
        bordered
        title="权限概览"
        actions={<span className="permission-dashboard-card-status">{loaded ? '数据已更新' : loading ? '正在加载' : '等待加载'}</span>}
      >
        <Descriptions
          bordered
          column={3}
          items={[
            { label: '数据状态', content: loaded ? '数据已更新' : loading ? '正在加载' : '等待加载' },
            { label: '可用角色', content: enabledRoleCount },
            { label: '权限节点总数', content: loading ? '...' : totalPermissionCount },
            { label: '数据来源', content: 'Permission Center API' },
          ]}
        />
      </Card>

      <Card className="permission-card" bordered title="操作流程" subtitle="按以下顺序完成服务资源授权">
        <Steps
          layout="vertical"
          readOnly
          current={-1}
          options={[
            { title: '建立角色', content: '定义角色编码、名称与职责范围。', icon: '01' },
            { title: '维护菜单与按钮', content: '搭建导航树，并为按钮记录 API 路径。', icon: '02' },
            { title: '授予角色权限', content: '将菜单和按钮节点授权给角色。', icon: '03' },
            { title: '绑定用户角色', content: '使用统一登录用户的 subject 完成角色绑定。', icon: '04' },
          ]}
        />
        <Space className="permission-dashboard-note">
          <Tag theme="warning" variant="light">提示</Tag>
          <span>统一登录只负责身份认证，角色和权限数据由本权限中心维护。</span>
          <Button variant="text" theme="primary" type="button" onClick={() => void loadSummary(serviceResource)}>刷新摘要</Button>
        </Space>
      </Card>
    </div>
  );
};

export default DashboardPage;
