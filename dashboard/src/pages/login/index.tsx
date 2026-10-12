import './style.less';

import { Badge, Button, Card, MessagePlugin, Tooltip } from 'tdesign-react';
import { MoonIcon, SunnyIcon } from 'tdesign-icons-react';
import { useEffect, useState } from 'react';
import { useLocation } from 'react-router-dom';

import {
  checkAuthenticated,
  getAuthenticatedLandingPath,
  rememberLoginRedirect,
  setCachedAuthStatus,
} from '../../router/auth';
import { startLogin } from '../../api/login';
import { applyThemeMode, getThemeMode } from '../../theme';
import BrandComponent from '../../components/brand';

const Login = () => {
  const location = useLocation();
  const [loading, setLoading] = useState(false);
  const [themeMode, setThemeMode] = useState<'light' | 'dark'>(() => getThemeMode());

  useEffect(() => {
    void checkAuthStatus();
  }, []);

  async function checkAuthStatus() {
    setCachedAuthStatus(false);
    const authenticated = await checkAuthenticated();
    setCachedAuthStatus(authenticated);
    if (authenticated) {
      const redirect = new URLSearchParams(location.search).get('redirect');
      window.location.replace(getAuthenticatedLandingPath(redirect));
    }
  }

  const handleToggleTheme = () => {
    const nextTheme = themeMode === 'light' ? 'dark' : 'light';
    setThemeMode(nextTheme);
    applyThemeMode(nextTheme);
  };

  const handleLogin = async () => {
    try {
      setLoading(true);
      rememberLoginRedirect(new URLSearchParams(location.search).get('redirect'));
      const result = await startLogin();

      if (result.authorizeUrl) {
        window.location.href = result.authorizeUrl;
      }
    } catch (error) {
      MessagePlugin.error(error instanceof Error ? error.message : '登录失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className={`stitch-login-page stitch-login-page--${themeMode}`}>
      <div className="stitch-login-bg" />

      <header className="stitch-login-header">
        <BrandComponent className="stitch-login-brand" />
        <Tooltip content={themeMode === 'light' ? '切换到暗色主题' : '切换到浅色主题'}>
          <Button
            className="stitch-login-theme-button"
            shape="circle"
            variant="text"
            aria-label={themeMode === 'light' ? '切换到暗色主题' : '切换到浅色主题'}
            icon={themeMode === 'light' ? <MoonIcon /> : <SunnyIcon />}
            onClick={handleToggleTheme}
          />
        </Tooltip>
      </header>

      <main className="stitch-login-main">
        <Card className="stitch-login-card" bordered={false}>
          <div className="stitch-login-identity">
            <h1 className="stitch-login-title">欢迎回来</h1>
            <p className="stitch-login-subtitle">使用统一登录服务验证身份后进入权限中心</p>
          </div>

          <Button className="stitch-login-submit" theme="primary" size="large" loading={loading} onClick={handleLogin} block>
            统一登录
          </Button>
        </Card>

        <div className="stitch-login-status">
          <Badge dot color="var(--app-success)">系统在线</Badge>
        </div>
      </main>

      <footer className="stitch-login-footer">
        <div>权限中心</div>
      </footer>

      <div className="stitch-login-side-visual" />
    </div>
  );
};

export default Login;
