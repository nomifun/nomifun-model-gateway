// SPDX-License-Identifier: Apache-2.0
// Adapted from codevisual/src/components/visuals/command-palette.tsx.
// Source SHA-256: cbe5d78a70768fad6b282fdbc210966542a8b3303295524da5546294a47bd666
// Copyright owner's explicit authorization permits Apache-2.0 distribution.
// Modifications: real navigation, controlled search, grouped results, keyboard
// selection, accessible combobox/listbox, focus-trapped dialog, reduced motion.
import { Modal } from '@arco-design/web-react';
import { CornerDownLeft, Search } from 'lucide-react';
import { motion, useReducedMotion } from 'motion/react';
import { useEffect, useId, useMemo, useRef, useState } from 'react';
import type { KeyboardEvent, ReactNode } from 'react';

export type PaletteItem = {
  id: string;
  label: string;
  group: string;
  icon: ReactNode;
};

export type CommandPaletteProps = {
  items: PaletteItem[];
  query: string;
  onQueryChange: (query: string) => void;
  onSelect: (id: string) => void;
  onClose: () => void;
  title: string;
  placeholder: string;
  noResultsLabel: string;
  navigateLabel: string;
  selectLabel: string;
  closeLabel: string;
  visible?: boolean;
};

function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="cv-command-kbd">{children}</kbd>;
}

export function CommandPalette({
  items, query, onQueryChange, onSelect, onClose, title, placeholder,
  noResultsLabel, navigateLabel, selectLabel, closeLabel, visible = true,
}: CommandPaletteProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const selectedRef = useRef<HTMLButtonElement>(null);
  const openerRef = useRef<HTMLElement | null>(typeof document === 'undefined' ? null : document.activeElement as HTMLElement);
  const restoreFocusRef = useRef(true);
  const reducedMotion = useReducedMotion();
  const listId = useId();
  const [selectedIndex, setSelectedIndex] = useState(0);
  const groups = useMemo(() => {
    const grouped = new Map<string, PaletteItem[]>();
    for (const item of items) grouped.set(item.group, [...(grouped.get(item.group) ?? []), item]);
    return [...grouped].map(([label, entries]) => ({ label, items: entries }));
  }, [items]);
  // Selection and rendering share the same order even if source groups interleave.
  const flatItems = groups.flatMap(group => group.items);
  const itemIds = flatItems.map(item => item.id).join('\u0000');
  const activeIndex = Math.min(selectedIndex, Math.max(flatItems.length - 1, 0));
  const selectedItem = flatItems[activeIndex];
  const optionId = (id: string) => `${listId}-${encodeURIComponent(id)}`;

  useEffect(() => { setSelectedIndex(0); }, [query, itemIds]);
  useEffect(() => { if (visible) inputRef.current?.focus({ preventScroll: true }); }, [visible]);
  useEffect(() => { selectedRef.current?.scrollIntoView({ block: 'nearest' }); }, [activeIndex]);
  useEffect(() => () => { if (restoreFocusRef.current && openerRef.current?.isConnected) openerRef.current.focus({ preventScroll: true }); }, []);
  const selectPage = (id: string) => { restoreFocusRef.current = false; onSelect(id); };

  const handleKey = (event: KeyboardEvent<HTMLElement>) => {
    if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (!flatItems.length) return;
      const delta = event.key === 'ArrowDown' ? 1 : -1;
      setSelectedIndex((activeIndex + delta + flatItems.length) % flatItems.length);
    } else if (event.key === 'Enter' && selectedItem) {
      event.preventDefault();
      selectPage(selectedItem.id);
    }
  };

  return <Modal
    visible={visible} title={title} onCancel={onClose} footer={null}
    focusLock autoFocus maskClosable={false} unmountOnExit
    afterOpen={() => inputRef.current?.focus({ preventScroll: true })}
    className="cv-command-modal" style={{ width: 'min(560px, calc(100vw - 32px))' }}
  >
    <div className="cv-command">
      <motion.div className="cv-command-frame"
        initial={false}
        animate={{ opacity: 1 }}
        transition={{ duration: reducedMotion ? 0 : 0.18 }}>
        <div className="cv-command-search">
          <Search aria-hidden="true" strokeWidth={2.2} />
          <input ref={inputRef} autoFocus role="combobox" aria-label={title}
            aria-autocomplete="list" aria-expanded={visible} aria-controls={listId}
            aria-activedescendant={selectedItem ? optionId(selectedItem.id) : undefined}
            autoComplete="off" spellCheck={false} value={query} placeholder={placeholder}
            onChange={event => onQueryChange(event.target.value)} onKeyDown={handleKey} />
          <span className="cv-command-count" aria-hidden="true">{flatItems.length}</span>
        </div>
        <div className="cv-command-results-frame">
          <div id={listId} className="cv-command-results" role="listbox" aria-label={title} onKeyDown={handleKey}>
            {!flatItems.length && <p className="cv-command-empty" role="status">{noResultsLabel}</p>}
            {groups.map(group => <div key={group.label} className="cv-command-group" role="group" aria-label={group.label}>
              <div className="cv-command-group-label">{group.label}</div>
              {group.items.map(item => {
                const index = flatItems.indexOf(item);
                const selected = index === activeIndex;
                return <motion.button key={item.id} id={optionId(item.id)}
                  ref={selected ? selectedRef : undefined} type="button" role="option"
                  aria-selected={selected} className={`cv-command-item${selected ? ' is-selected' : ''}`}
                  onMouseEnter={() => setSelectedIndex(index)} onFocus={() => setSelectedIndex(index)}
                  onClick={() => selectPage(item.id)}
                  initial={reducedMotion ? false : { opacity: 0, y: 4 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ duration: reducedMotion ? 0 : 0.15, delay: reducedMotion ? 0 : Math.min(index, 6) * 0.018 }}>
                  <span className="cv-command-selection" aria-hidden="true" />
                  <span className="cv-command-item-icon" aria-hidden="true">{item.icon}</span>
                  <span className="cv-command-item-label">{item.label}</span>
                  <span className="cv-command-item-enter" aria-hidden="true"><CornerDownLeft strokeWidth={2.4} /></span>
                </motion.button>;
              })}
            </div>)}
          </div>
          <div className="cv-command-footer">
            <div className="cv-command-footer-actions">
              <span><Kbd>↑</Kbd><Kbd>↓</Kbd><span>{navigateLabel}</span></span>
              <span><Kbd>↵</Kbd><span>{selectLabel}</span></span>
            </div>
            <span><Kbd>Esc</Kbd><span>{closeLabel}</span></span>
          </div>
        </div>
      </motion.div>
    </div>
  </Modal>;
}
