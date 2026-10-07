// SPDX-License-Identifier: Apache-2.0
// Adapted from the user's codevisual/src/components/visuals/stat-card.tsx.
// Source SHA256: 4FF84284B8510281ED7D6C9AB78E2AAE468BBF91D1B058FECAAA90E0865B5A29.
// The copyright owner authorized this component's migration under Apache-2.0.
// Modifications: semantic CSS, required live values, accessible content and actions,
// reduced-motion support and a single mount animation; removed demo data and effects.
import { DollarSign } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';
import type { Variants } from 'motion/react';
import type { ReactNode } from 'react';

export type StatCardProps = {
  label: ReactNode;
  value: ReactNode;
  icon?: ReactNode;
  hint?: ReactNode;
  footer?: ReactNode;
  action?: ReactNode;
  animated?: boolean;
  className?: string;
};

const frameVariants: Variants = {
  hidden: { opacity: 0, y: 8 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.35, ease: 'easeOut' } },
};
const iconVariants: Variants = {
  hidden: { opacity: 0, scale: 0 },
  visible: { opacity: 1, scale: 1, transition: { type: 'spring', stiffness: 450, damping: 18, delay: 0.3 } },
};
const labelVariants: Variants = {
  hidden: { opacity: 0, y: 4 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.25, delay: 0.35 } },
};
const valueVariants: Variants = {
  hidden: { opacity: 0, y: 8 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.35, delay: 0.45, ease: 'easeOut' } },
};
const footVariants: Variants = {
  hidden: { opacity: 0, y: 4 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.25, delay: 0.55 } },
};

export function StatCard({ label, value, icon, hint, footer, action, animated = true, className }: StatCardProps) {
  const reducedMotion = useReducedMotion();
  const animate = animated && !reducedMotion;
  const motionProps = { initial: animate ? 'hidden' as const : false as const, animate: 'visible' as const };
  return <motion.div className={['cv-frame', 'cv-stat-card', className].filter(Boolean).join(' ')} variants={animate ? frameVariants : undefined} {...motionProps}>
    <div className="cv-surface cv-stat-surface">
      <div className="cv-stat-heading">
        <motion.span className="cv-icon" aria-hidden="true" variants={animate ? iconVariants : undefined} {...motionProps}>{icon ?? <DollarSign size={16} />}</motion.span>
        <motion.span className="cv-label" variants={animate ? labelVariants : undefined} {...motionProps}>{label}</motion.span>
      </div>
      <motion.div className="cv-stat-value" variants={animate ? valueVariants : undefined} {...motionProps}>{value}</motion.div>
      {hint != null && <motion.div className="cv-stat-hint" variants={animate ? footVariants : undefined} {...motionProps}>{hint}</motion.div>}
      {(footer != null || action != null) && <motion.div className="cv-stat-footer" variants={animate ? footVariants : undefined} {...motionProps}>
        {footer != null && <div className="cv-stat-footer-copy">{footer}</div>}
        {action != null && <div className="cv-stat-action">{action}</div>}
      </motion.div>}
    </div>
  </motion.div>;
}
