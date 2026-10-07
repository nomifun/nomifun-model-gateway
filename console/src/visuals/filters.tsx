// SPDX-License-Identifier: Apache-2.0
// Adapted from codevisual/src/components/visuals/filters.tsx.
// Source SHA-256: d4798d3ecf7924a069fdd550139f21410ed27ba2469d5cfd45661a3594295890
// Copyright owner's explicit authorization permits Apache-2.0 distribution.
// Modifications: controlled search and filter slots, removable active filters,
// real result counts, keyboard actions, responsive layout and reduced motion.
import { Search, SlidersHorizontal, X } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';
import type { ReactNode } from 'react';

export type ActiveFilter = {
  id?: string;
  label: string;
  onRemove: () => void;
};

export type FiltersProps = {
  query: string;
  onQuery: (query: string) => void;
  placeholder: string;
  queryLabel?: string;
  title: string;
  clearLabel: string;
  removeLabel: string;
  slots?: ReactNode;
  activeFilters?: ActiveFilter[];
  onClear?: () => void;
  count: ReactNode;
  className?: string;
};

export function Filters({
  query, onQuery, placeholder, queryLabel, title, clearLabel, removeLabel,
  slots, activeFilters = [], onClear, count, className,
}: FiltersProps) {
  const reducedMotion = useReducedMotion();
  const hasFilters = Boolean(query.trim() || activeFilters.length);

  return <div className={`cv-filters${className ? ` ${className}` : ''}`}>
    <motion.div className="cv-filters-frame"
      initial={false}
      animate={{ opacity: 1 }}
      transition={{ duration: reducedMotion ? 0 : 0.18 }}>
      <div className="cv-filters-card">
        <div className="cv-filters-header">
          <div className="cv-filters-heading"><SlidersHorizontal aria-hidden="true" strokeWidth={2.2} /><span>{title}</span></div>
          {onClear && <button type="button" className="cv-filters-clear" disabled={!hasFilters} onClick={onClear}>{clearLabel}</button>}
        </div>
        <div className="cv-filters-body">
          <div className="cv-filters-controls">
            <div className="cv-filters-search">
              <Search aria-hidden="true" strokeWidth={2.2} />
              <input aria-label={queryLabel ?? placeholder} value={query} placeholder={placeholder}
                onChange={event => onQuery(event.target.value)} />
              {query && <button type="button" className="cv-filters-search-clear" aria-label={clearLabel} title={clearLabel} onClick={() => onQuery('')}><X aria-hidden="true" /></button>}
            </div>
            {slots && <div className="cv-filters-slots">{slots}</div>}
          </div>
          {activeFilters.length > 0 && <div className="cv-filters-rules">
            {activeFilters.map((filter, index) => <motion.div key={filter.id ?? `${filter.label}-${index}`} className="cv-filters-rule"
              initial={reducedMotion ? false : { opacity: 0, x: -6 }}
              animate={{ opacity: 1, x: 0 }}
              transition={{ duration: reducedMotion ? 0 : 0.15 }}>
              <span className="cv-filters-rule-value">{filter.label}</span>
              <button type="button" className="cv-filters-rule-remove" aria-label={`${removeLabel} ${filter.label}`} title={`${removeLabel} ${filter.label}`} onClick={filter.onRemove}><X aria-hidden="true" strokeWidth={2.5} /></button>
            </motion.div>)}
          </div>}
        </div>
        <div className="cv-filters-footer" role="status" aria-live="polite"><span className="cv-filters-count">{count}</span></div>
      </div>
    </motion.div>
  </div>;
}
