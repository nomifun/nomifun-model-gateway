// SPDX-License-Identifier: Apache-2.0
// Adapted from codevisual/src/components/visuals/toast.tsx.
// Source SHA-256: 2c65a006bdd9cd1910136761175d0e21b799c4232b4a35fb9529983856378686.
// The original component's copyright owner authorized Apache-2.0 redistribution.
// Modifications: real feedback and close action, keyboard access, local theme
// classes and reduced-motion support; removed demo defaults and repeat animation.
import { AlertTriangle, CheckCircle2, Info, X } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';

export type ToastKind = 'success' | 'warning' | 'info';
export type ToastProps = {
  kind: ToastKind;
  title: string;
  body?: string;
  closeLabel?: string;
  onClose?: () => void;
  className?: string;
};
const KIND = { success: CheckCircle2, warning: AlertTriangle, info: Info };

export function Toast({ kind, title, body, closeLabel, onClose, className = '' }: ToastProps) {
  const reducedMotion = useReducedMotion();
  const Icon = KIND[kind];
  return <div className={`cv-toast cv-toast-${kind} ${className}`}>
    <motion.div className="cv-toast-stack" initial={reducedMotion ? false : { opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.25 }}>
      <div className="cv-toast-glow" aria-hidden="true" />
      <div className="cv-toast-back cv-toast-back-far cv-surface" aria-hidden="true" />
      <div className="cv-toast-back cv-surface" aria-hidden="true" />
      <div className="cv-toast-card cv-surface">
        <span className="cv-toast-icon" aria-hidden="true"><Icon size={20} /></span>
        <div className="cv-toast-copy"><span className="cv-toast-title">{title}</span>{body && <span className="cv-toast-body">{body}</span>}</div>
        {onClose && closeLabel && <button className="cv-toast-close" type="button" aria-label={closeLabel} onClick={onClose}><X size={15} aria-hidden="true" /></button>}
      </div>
    </motion.div>
  </div>;
}
