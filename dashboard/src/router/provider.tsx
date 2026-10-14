import { useEffect, useState } from 'react';
import { RouterProvider } from 'react-router-dom';
import { Alert, Button, Space } from 'tdesign-react';
import { getMenuList } from '../api/auth';
import GlobalLoading from '../components/global-loading';
import { WorkbenchNavigationContext } from '../layouts/layout/workbench-navigation';
import { buildWorkbenchMenus, type WorkbenchMenu } from './workbench-menu';
import { createWorkbenchRouter } from '.';

type Navigation = { router: ReturnType<typeof createWorkbenchRouter>; menus: WorkbenchMenu[] };

export default function RootRouterProvider() {
  const [navigation, setNavigation] = useState<Navigation | null>(null);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let disposed = false;
    setError('');
    getMenuList().then(records => {
      if (disposed) return;
      const menus = buildWorkbenchMenus(records);
      setNavigation({ router: createWorkbenchRouter(menus), menus });
    }).catch(cause => {
      if (!disposed) setError(cause instanceof Error ? cause.message : '请稍后重试');
    });
    return () => { disposed = true; };
  }, [attempt]);

  if (error) return <Space direction="vertical" style={{ padding: 24 }}>
    <Alert theme="error" title="加载导航菜单失败" message={error} operation={<Button variant="text" onClick={() => setAttempt(value => value + 1)}>重试</Button>} />
  </Space>;
  if (!navigation) return <GlobalLoading />;

  return <WorkbenchNavigationContext.Provider value={navigation.menus}>
    <RouterProvider router={navigation.router} />
  </WorkbenchNavigationContext.Provider>;
}
