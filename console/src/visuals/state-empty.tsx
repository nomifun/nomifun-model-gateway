// SPDX-License-Identifier: Apache-2.0
// Adapted from codevisual/src/components/visuals/state-empty.tsx.
// Source SHA-256: c87b8c67e90557959c29c0dcab8c8c3ff3bc1f4e74f5acd2368660600419fd3e.
// The original component's copyright owner authorized Apache-2.0 redistribution.
// Modifications: semantic status content, real actions, local theme classes,
// reduced-motion support and one-time entry; removed demo data, loops and transforms.
import { Plus } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';
import type { ReactNode } from 'react';

export type StateEmptyProps = {
  title: string;
  description?: string;
  icon?: ReactNode;
  action?: ReactNode;
  actionLabel?: string;
  onAction?: () => void;
  compact?: boolean;
  className?: string;
};

function SlotSkeleton() {
  return <div className="cv-empty-slot">
    <div className="cv-empty-slot-header"><div className="cv-empty-avatar" /><div className="cv-empty-lines"><span /><span /></div></div>
    <div className="cv-empty-tags"><span /><span /></div>
  </div>;
}

export function StateEmpty({ title, description, icon, action, actionLabel, onAction, compact = false, className = '' }: StateEmptyProps) {
  const reducedMotion = useReducedMotion();
  return <section className={`cv-empty ${compact ? 'cv-empty-compact' : ''} ${className}`} aria-label={title}>
    <motion.div className="cv-empty-illustration" initial={reducedMotion ? false : { opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.25 }}>
      <div className="cv-empty-frame cv-frame">
        <div className="cv-empty-glow" aria-hidden="true" />
        <div className="cv-empty-panel cv-surface">
          <div className="cv-empty-grid" aria-hidden="true">{Array.from({ length: 4 }, (_, index) => <SlotSkeleton key={index} />)}</div>
          <div className="cv-empty-add-wrap">
            {onAction && actionLabel
              ? <button className="cv-empty-add" type="button" aria-label={actionLabel} onClick={onAction}><Plus size={17} strokeWidth={2.5} aria-hidden="true" /></button>
              : <span className="cv-empty-add" aria-hidden="true">{icon ?? <Plus size={17} strokeWidth={2.5} />}</span>}
          </div>
        </div>
      </div>
    </motion.div>
    <div className="cv-empty-content"><h3>{title}</h3>{description && <p>{description}</p>}{action ? <div className="cv-empty-action">{action}</div> : onAction && actionLabel && <button className="cv-empty-action cv-button" type="button" onClick={onAction}>{actionLabel}</button>}</div>
  </section>;
}
