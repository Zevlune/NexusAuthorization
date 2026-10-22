import { Button, Empty } from 'tdesign-react';
import { isRouteErrorResponse, useRouteError } from 'react-router-dom';
import ErrorPage from '.';

// Shown when rendering or lazy-loading a route fails. A missing route still shows the 404 page;
// anything else (for example a chunk removed by a new release) offers a reload instead of "404".
const RouteError = () => {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) return <ErrorPage />;
  return <Empty
    type="fail"
    title="页面加载失败"
    description="页面加载出错，可能是系统已更新或网络异常，请刷新后重试。"
    style={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', height: '100%' }}
    action={<Button theme="primary" onClick={() => window.location.reload()}>刷新页面</Button>}
  />;
};
export default RouteError;
