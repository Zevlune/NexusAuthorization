import './style.less';

interface BrandComponentProps {
  className?: string;
  compact?: boolean;
}

const BrandComponent = ({ className, compact = false }: BrandComponentProps) => {
  const classes = ['brand', compact ? 'brand--compact' : '', className || '']
    .filter(Boolean)
    .join(' ');

  return (
    <div className={classes} aria-label="权限中心">
      <span className="brand__mark" aria-hidden="true">PC</span>
      {!compact && (
        <span className="brand__copy">
          <span className="brand__name">权限中心</span>
          <span className="brand__product">PERMISSION CENTER</span>
        </span>
      )}
    </div>
  );
};

export default BrandComponent;
