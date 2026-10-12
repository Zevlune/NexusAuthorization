import { Loading } from 'tdesign-react';

interface GlobalLoadingProps {
  height?: string | number;
}

const GlobalLoading = ({ height = '100vh' }: GlobalLoadingProps) => (
  <Loading fullscreen loading size="medium" text="页面加载中..." zIndex={999} style={{ minHeight: height }} />
);
export default GlobalLoading;
