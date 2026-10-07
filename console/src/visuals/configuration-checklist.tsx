// SPDX-License-Identifier: Apache-2.0
// Adapted from user-owned codevisual/src/components/visuals/health-check.tsx.
// Source SHA256: B99BA9572189D7EF0008201531E976A3FC6297BECB346B5FDC210F48F1EDF836.
// The copyright owner authorized Apache-2.0 transplantation on 2026-10-07.
// Modifications: real credential-presence props instead of simulated health,
// no latency or demo services, accessible content, responsive semantic styles,
// and a single reduced-motion-aware entrance instead of repeating effects.
import type { ReactNode } from 'react';
import { Check, KeyRound, Minus } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';

export type ConfigurationItem = { id: string; label: string; present: boolean; icon?: ReactNode };
export function ConfigurationChecklist({ title, summary, items, presentLabel, missingLabel }: {
  title: string; summary: string; items: ConfigurationItem[]; presentLabel: string; missingLabel: string;
}) {
  const reduceMotion = useReducedMotion();
  return <motion.div className="cv-frame cv-checklist" initial={reduceMotion ? false : { opacity: 0, y: 5 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: .2 }}>
    <div className="cv-surface"><div className="cv-checklist-heading"><span className="cv-checklist-title"><KeyRound aria-hidden="true" size={15} />{title}</span><span className="cv-checklist-summary">{summary}</span></div>
      <div className="cv-checklist-rows">{items.map(item => <div className="cv-checklist-row" key={item.id}><span className="cv-checklist-icon" aria-hidden="true">{item.icon ?? <KeyRound size={15} />}</span><span className="cv-checklist-name">{item.label}</span><span className={'cv-checklist-state ' + (item.present ? 'present' : 'missing')}>{item.present ? <Check size={13} aria-hidden="true" /> : <Minus size={13} aria-hidden="true" />}{item.present ? presentLabel : missingLabel}</span></div>)}</div>
    </div>
  </motion.div>;
}
