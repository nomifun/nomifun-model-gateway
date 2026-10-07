// SPDX-License-Identifier: Apache-2.0
// Adapted from the user's codevisual/src/components/visuals/usage-meter.tsx.
// Source SHA256: BC082B89CAAABECCF4B785754D084F74BE7A11C3ED00EF2D9ADD8B0DE055ECC4.
// The copyright owner authorized this component's migration under Apache-2.0.
// Modifications: semantic CSS, required live values, accessible progress and actions,
// reduced-motion support and a single mount animation; removed demo data and effects.
import { Activity } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';
import type { Variants } from 'motion/react';
import type { ReactNode } from 'react';

export type UsageRow = {
  id: string;
  label: ReactNode;
  detail?: ReactNode;
  amount: ReactNode;
  color?: string;
};
export type UsageMeterProps = {
  title: ReactNode;
  value: ReactNode;
  icon?: ReactNode;
  period?: ReactNode;
  quotaLabel?: ReactNode;
  percent?: number | null;
  valueText?: string;
  meterLabel?: string;
  hint?: ReactNode;
  rows?: UsageRow[];
  footer?: ReactNode;
  action?: ReactNode;
  animated?: boolean;
  className?: string;
};

const frameVariants: Variants = {
  hidden: { opacity: 0 },
  visible: { opacity: 1, transition: { duration: 0.25, ease: 'easeOut' } },
};
const headVariants: Variants = {
  hidden: { opacity: 0 },
  visible: { opacity: 1, transition: { duration: 0.3, delay: 0.2 } },
};
const rowVariants: Variants = {
  hidden: { opacity: 0, y: 8 },
  visible: (index: number) => ({ opacity: 1, y: 0, transition: { duration: 0.3, delay: 0.6 + index * 0.12, ease: 'easeOut' } }),
};
const footVariants: Variants = {
  hidden: { opacity: 0, y: 6 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.3, delay: 0.7 } },
};

export function UsageMeter({ title, value, icon, period, quotaLabel, percent, valueText, meterLabel, hint, rows, footer, action, animated = true, className }: UsageMeterProps) {
  const reducedMotion = useReducedMotion();
  const animate = animated && !reducedMotion;
  const motionProps = { initial: animate ? 'hidden' as const : false as const, animate: 'visible' as const };
  const progress = typeof percent === 'number' && Number.isFinite(percent) ? Math.min(100, Math.max(0, percent)) : undefined;
  return <motion.div className={['cv-frame', 'cv-usage-meter', className].filter(Boolean).join(' ')} variants={animate ? frameVariants : undefined} {...motionProps}>
    <div className="cv-surface cv-usage-surface">
      <motion.div className="cv-usage-heading" variants={animate ? headVariants : undefined} {...motionProps}>
        <div className="cv-usage-heading-title"><span className="cv-icon" aria-hidden="true">{icon ?? <Activity size={16} strokeWidth={2.5} />}</span><span className="cv-label">{title}</span></div>
        {period != null && <span className="cv-usage-period">{period}</span>}
      </motion.div>
      <div className="cv-usage-quota">
        {quotaLabel != null && <div className="cv-usage-quota-label">{quotaLabel}</div>}
        <div className="cv-usage-value">{value}</div>
        {progress !== undefined && <div className="cv-usage-track" role="progressbar" aria-label={meterLabel ?? (typeof title === 'string' ? title : undefined)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress} aria-valuetext={valueText}>
          <motion.div className="cv-usage-fill" style={{ width: `${progress}%` }} initial={animate ? { scaleX: 0 } : false} animate={{ scaleX: 1 }} transition={animate ? { duration: 0.7, delay: 0.5, ease: 'easeOut' } : { duration: 0 }} />
        </div>}
        {hint != null && <div className="cv-usage-hint">{hint}</div>}
      </div>
      {!!rows?.length && <div className="cv-usage-rows">{rows.map((row, index) => <motion.div key={row.id} className="cv-usage-row" variants={animate ? rowVariants : undefined} custom={index} {...motionProps}>
        <div className="cv-usage-row-copy">{row.color && <span className="cv-usage-row-dot" style={{ backgroundColor: row.color }} aria-hidden="true" />}<span className="cv-usage-row-label">{row.label}</span>{row.detail != null && <span className="cv-usage-row-detail">{row.detail}</span>}</div>
        <span className="cv-usage-row-amount">{row.amount}</span>
      </motion.div>)}</div>}
      {(footer != null || action != null) && <motion.div className="cv-usage-footer" variants={animate ? footVariants : undefined} {...motionProps}>
        {footer != null && <div className="cv-usage-footer-copy">{footer}</div>}
        {action != null && <div className="cv-usage-action">{action}</div>}
      </motion.div>}
    </div>
  </motion.div>;
}
