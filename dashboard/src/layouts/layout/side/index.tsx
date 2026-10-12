import MenuComponent from './menu';
import type { NavigationMenu } from '../../../router/dynamic-routes';

interface SliderMenuProps {
    collapse: boolean;
    menus: NavigationMenu[];
}

const SliderMenu = ({ collapse, menus }: SliderMenuProps) => {
    return (
        <MenuComponent collapse={collapse} menus={menus} />
    )
}
export default SliderMenu
