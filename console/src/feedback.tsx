// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState } from 'react';
import type { ReactNode } from 'react';
import { Alert, Button, Modal, Space } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import { Toast } from './visuals/toast';

type Notice = { type: 'success' | 'warning'; message: string };
type Confirmation = { title: string; onOk: () => unknown | Promise<unknown> };
type Feedback = { success: (message: string) => void; warning: (message: string) => void; confirm: (value: Confirmation) => void };
const FeedbackContext = createContext<Feedback | null>(null);

// Arco's static Modal/Message APIs use legacy ReactDOM.render. Every feedback
// element here belongs to the existing React root and works with React 19.
export function FeedbackNotice({ notice, close }: { notice: Notice; close?: () => void }) {
  const { t } = useTranslation();
  return <div className="feedback-notice" role="status" aria-live="polite"><Toast kind={notice.type} title={notice.message} closeLabel={t('done')} onClose={close} /></div>;
}
export function FeedbackProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [notice, setNotice] = useState<Notice>(); const [confirmation, setConfirmation] = useState<Confirmation>(); const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  const feedback: Feedback = {
    success: message => setNotice({ type: 'success', message }),
    warning: message => setNotice({ type: 'warning', message }),
    confirm: value => { setError(''); setConfirmation(value); }
  };
  const confirm = async () => {
    if (!confirmation) return;
    setBusy(true); setError('');
    try { await confirmation.onOk(); setConfirmation(undefined); } catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { setBusy(false); }
  };
  return <FeedbackContext.Provider value={feedback}>{children}{notice && <FeedbackNotice notice={notice} close={() => setNotice(undefined)} />}
    {confirmation && <Modal visible title={confirmation.title} onCancel={() => { if (!busy) setConfirmation(undefined); }} closable={!busy} maskClosable={!busy} footer={<Space><Button disabled={busy} onClick={() => setConfirmation(undefined)}>{t('cancel')}</Button><Button status="danger" type="primary" loading={busy} onClick={confirm}>{t('confirm')}</Button></Space>}>
      {error && <Alert type="error" content={t(error)} />}{confirmation.title}
    </Modal>}
  </FeedbackContext.Provider>;
}
export function useFeedback(): Feedback {
  const feedback = useContext(FeedbackContext);
  if (!feedback) throw new Error('Missing feedback provider');
  return feedback;
}
