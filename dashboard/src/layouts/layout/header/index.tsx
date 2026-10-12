import { useEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Button, Space, Tooltip } from 'tdesign-react';
import { Fullscreen1Icon, FullscreenExit1Icon, MoonIcon, SunnyIcon } from 'tdesign-icons-react';
import { APPLICATION_CHANGE_EVENT, getStoredApplication } from '../../../api/application';

interface PublicHeaderProps {
  theme: 'light' | 'dark';
  onChangeTheme: () => void;
}

const PublicHeader = ({ theme, onChangeTheme }: PublicHeaderProps) => {
    const location = useLocation();
    const navigate = useNavigate();
    const [application, setApplication] = useState(getStoredApplication);
    const [isFullscreen, setIsFullscreen] = useState(Boolean(document.fullscreenElement));

    useEffect(() => {
        const syncApplication = () => {
            setApplication(getStoredApplication());
        };
        const handleStorage = (event: StorageEvent) => {
            if (event.key === 'permission-center-application') syncApplication();
        };

        window.addEventListener(APPLICATION_CHANGE_EVENT, syncApplication);
        window.addEventListener('storage', handleStorage);
        return () => {
            window.removeEventListener(APPLICATION_CHANGE_EVENT, syncApplication);
            window.removeEventListener('storage', handleStorage);
        };
    }, []);

    useEffect(() => {
        const handleFullscreenChange = () => {
            setIsFullscreen(Boolean(document.fullscreenElement));
        };

        document.addEventListener('fullscreenchange', handleFullscreenChange);
        return () => {
            document.removeEventListener('fullscreenchange', handleFullscreenChange);
        };
    }, []);

    const handleToggleFullscreen = async () => {
        if (document.fullscreenElement) {
            await document.exitFullscreen();
            setIsFullscreen(false);
            return;
        }
        await document.documentElement.requestFullscreen();
        setIsFullscreen(true);
    };

    const handleSwitchApplication = () => {
        const redirect = `${location.pathname}${location.search}${location.hash}`;
        navigate(`/select-application?redirect=${encodeURIComponent(redirect)}`);
    };

    return (
        <div className="layout-header-edit" >
            <Space size="medium">
                <div
                  className="layout-current-service-resource"
                  title={application ? `当前应用：${application}` : '当前未选择应用'}
                >
                  <span className="layout-current-service-resource-label">业务应用</span>
                  <strong className="layout-current-service-resource-value">
                    {application || '未选择'}
                  </strong>
                </div>
                <Button
                  className="layout-service-resource-switch"
                  variant="text"
                  size="small"
                  onClick={handleSwitchApplication}
                >
                  切换应用
                </Button>
                <Tooltip
                  placement="bottom"
                  trigger="hover"
                  content={`点击切换为${theme === 'light' ? '暗黑' : '亮色'}模式`}
                >
                  <Button
                    className="layout-header-tool-button"
                    variant="text"
                    size="small"
                    shape="circle"
                    type="button"
                    aria-label={theme === 'light' ? '切换到暗色主题' : '切换到浅色主题'}
                    icon={theme === 'light' ? <MoonIcon size="18px" /> : <SunnyIcon size="18px" />}
                    onClick={onChangeTheme}
                  />
                </Tooltip>
                <Tooltip
                  placement="bottom"
                  trigger="hover"
                  content={isFullscreen ? '退出全屏' : '全屏显示'}
                >
                  <Button
                    className="layout-header-tool-button"
                    variant="text"
                    size="small"
                    shape="circle"
                    type="button"
                    aria-label={isFullscreen ? '退出全屏' : '进入全屏'}
                    icon={isFullscreen ? <FullscreenExit1Icon size="18px" /> : <Fullscreen1Icon size="18px" />}
                    onClick={() => {
                      void handleToggleFullscreen();
                    }}
                  />
                </Tooltip>
            </Space>
        </div>

    )
}
export default PublicHeader
