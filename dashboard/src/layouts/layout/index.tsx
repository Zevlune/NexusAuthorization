import './style.less';
import { Button, Layout, Tabs } from 'tdesign-react';
import { ChevronLeftIcon, ChevronRightIcon } from 'tdesign-icons-react';
import { applyThemeMode, getThemeMode } from '../../theme';
import { getPageLoading, subscribePageLoading } from '../../page-loading';
import { setPageLoading } from '../../page-loading';
import { Suspense, useEffect, useState, useSyncExternalStore } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import AvatarComponent from './avatar';
import GlobalLoading from '../../components/global-loading';
import LogoComponent from './logo';
import PublicHeader from './header';
import WorkbenchMenu from './side/workbench-menu';
import { getWorkbenchPageLabel, useWorkbenchNavigation } from './workbench-navigation';
import { DEFAULT_LAYOUT_TAB, getStoredLayoutTabs, LAYOUT_TABS_STORAGE_KEY, type LayoutTabItem } from './tab-storage';

const { Content, Aside, Header } = Layout;
const { TabPanel } = Tabs;

const PublicLayout = () => {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const menus = useWorkbenchNavigation();
  const pageLoading = useSyncExternalStore(subscribePageLoading, getPageLoading, getPageLoading);
  const [collapsed, setCollapsed] = useState(false);
  const [mobile, setMobile] = useState(() => window.matchMedia('(max-width: 640px)').matches);
  const compactNavigation = mobile || collapsed;
  const [theme, setTheme] = useState<'light' | 'dark'>(() => getThemeMode());
  const [tabs, setTabs] = useState<LayoutTabItem[]>(() => getStoredLayoutTabs().map((tab) => {
    return { ...tab, label: getWorkbenchPageLabel(menus, tab.value) };
  }));
  const currentLabel = getWorkbenchPageLabel(menus, pathname);

  useEffect(() => { window.localStorage.setItem(LAYOUT_TABS_STORAGE_KEY, JSON.stringify(tabs)); }, [tabs]);
  useEffect(() => {
    const media = window.matchMedia('(max-width: 640px)');
    const sync = () => setMobile(media.matches);
    media.addEventListener('change', sync);
    return () => media.removeEventListener('change', sync);
  }, []);
  useEffect(() => {
    setTabs((current) => {
      const visibleTabs = current.filter((tab) => tab.value !== '/bindings');
      if (pathname === '/bindings') return visibleTabs;
      return visibleTabs.some((tab) => tab.value === pathname)
        ? visibleTabs.map((tab) => tab.value === pathname ? { ...tab, label: currentLabel } : tab)
        : [...visibleTabs, { value: pathname, label: currentLabel, removable: pathname !== DEFAULT_LAYOUT_TAB.value }];
    });
    document.title = currentLabel;
  }, [currentLabel, pathname]);
  useEffect(() => { setPageLoading(false); }, [pathname]);

  const handleChangeTheme = () => {
    const next = theme === 'light' ? 'dark' : 'light';
    setTheme(next); applyThemeMode(next);
  };
  const handleTabRemove = ({ value }: { value: string | number }) => {
    const closing = String(value);
    if (closing === DEFAULT_LAYOUT_TAB.value) return;
    const next = tabs.filter((tab) => tab.value !== closing);
    setTabs(next);
    if (pathname === closing) navigate(next[next.length - 1]?.value || '/dashboard');
  };

  return (
    <Layout className="layout-container">
      {pageLoading && <GlobalLoading />}
      <Header className="layout-header">
        <div className="layout-header-left"><LogoComponent collapse={false} /></div>
        <div className="layout-header-right"><PublicHeader theme={theme} onChangeTheme={handleChangeTheme} /><AvatarComponent /></div>
      </Header>
      <Layout className="layout-body">
        <Aside width={compactNavigation ? (mobile ? '56px' : '64px') : '240px'} className={`layout-sider${compactNavigation ? ' is-collapsed' : ''}`}>
          <div className="layout-sider-menu"><WorkbenchMenu collapse={compactNavigation} /></div>
          <Button variant="text" className="layout-sider-trigger-bottom" type="button" aria-label={collapsed ? '展开导航' : '收起导航'} onClick={() => setCollapsed((value) => !value)}>{collapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}</Button>
        </Aside>
        <Layout className="layout-main">
          <Content className="layout-content">
            <div className="layout-content-tabs"><Tabs value={pathname} onChange={(value) => navigate(String(value))} onRemove={handleTabRemove}>{tabs.map((tab) => <TabPanel key={tab.value} value={tab.value} label={tab.label} removable={tab.removable} />)}</Tabs></div>
            <div className="layout-content-inner"><div className="layout-main-content"><Suspense fallback={<GlobalLoading />}><Outlet /></Suspense></div></div>
          </Content>
        </Layout>
      </Layout>
    </Layout>
  );
};

export default PublicLayout;
