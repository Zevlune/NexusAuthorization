import type { ReactNode } from 'react';
import { Alert, Breadcrumb, Button, Empty as TEmpty, MessagePlugin } from 'tdesign-react';
import { useLocation } from 'react-router-dom';
import { useWorkbenchNavigation } from '../../layouts/layout/workbench-navigation';
import { workbenchMenuTrail } from '../../router/workbench-menu';
import type { ApplicationServiceResource } from '../../api/application';
import type { Endpoint, Role, Menu, SubjectRole } from '../../api/rbac-v1';
import { getRequestErrorMessage } from '../nexus-authorization/shared';
export type State = { appKey: string; services: ApplicationServiceResource[]; endpoints: Endpoint[]; roles: Role[]; menus: Menu[]; bindings: SubjectRole[]; loading: boolean; loadError?: string; reload: () => Promise<void> };
export const fail = (error: unknown, fallback: string) => MessagePlugin.error(getRequestErrorMessage(error, fallback));
export const Page = ({ actions, children }: { title?: string; description?: string; actions?: ReactNode; children: ReactNode }) => {
  const location = useLocation();
  const menus = useWorkbenchNavigation();
  const trail = workbenchMenuTrail(menus, location.pathname);
  return <div className="rbac-page"><div className="rbac-page-topline">{trail.length > 1 && <Breadcrumb className="rbac-breadcrumb" aria-label="当前位置" options={trail.map(menu => ({ content: menu.name }))} />}{actions ? <div className="rbac-page-header">{actions}</div> : null}</div>{children}</div>;
};
export const Empty = ({ text }: { text: string }) => <TEmpty className="rbac-empty" description={text} />;
export const Guard = ({ state, children }: { state: State; children: ReactNode }) => state.appKey ? <>{state.loadError && <Alert className="rbac-load-error" theme="error" message={state.loadError} operation={<Button variant="text" onClick={() => void state.reload()}>重试</Button>} />}{children}</> : <Page title="应用权限配置"><Empty text="尚未选择业务应用" /></Page>;
