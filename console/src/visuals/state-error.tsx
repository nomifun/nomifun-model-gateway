// SPDX-License-Identifier: Apache-2.0
// Adapted from codevisual/src/components/visuals/state-error.tsx.
// Source SHA-256: af50c605b1d36569a31f7dc910ef08cf344fafd6b639d8d600a382c61f5cd814.
// The original component's copyright owner authorized Apache-2.0 redistribution.
// Modifications: real error text and retry, semantic alert, local theme classes,
// reduced-motion support and one-time entry; removed fake statuses and loops.
import { MousePointerClick, RotateCw, TriangleAlert } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';

export type StateErrorProps = {
  title: string;
  message: string;
  help?: string;
  retryLabel?: string;
  onRetry?: () => void;
  className?: string;
};

export function StateError({ title, message, help, retryLabel, onRetry, className = '' }: StateErrorProps) {
  const reducedMotion = useReducedMotion();
  return <section className={`cv-error ${className}`} role="alert">
    <motion.div className="cv-error-illustration" aria-hidden="true" initial={reducedMotion ? false : { opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.25 }}>
      <div className="cv-error-node"><MousePointerClick size={20} /></div>
      <svg className="cv-error-connector" viewBox="0 0 100 12" fill="none"><path d="M 0,6 L 100,6" stroke="currentColor" strokeWidth="1" strokeDasharray="3 3" /><circle cx="48" cy="6" r="3" fill="currentColor" /></svg>
      <div className="cv-error-card cv-surface">
        {Array.from({ length: 3 }, (_, index) => <div className="cv-error-row" key={index}><div className="cv-error-glyph" /><div className="cv-error-lines"><span /><span /></div><div className="cv-error-dot" /></div>)}
        <span className="cv-error-alert"><TriangleAlert size={14} strokeWidth={2.5} /></span>
      </div>
    </motion.div>
    <div className="cv-error-content"><h3>{title}</h3><p>{message}</p>{help && <p className="cv-error-help">{help}</p>}</div>
    {onRetry && retryLabel && <button className="cv-error-action cv-button" type="button" onClick={onRetry}><RotateCw size={14} aria-hidden="true" />{retryLabel}</button>}
  </section>;
}
