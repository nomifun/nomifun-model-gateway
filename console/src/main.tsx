// SPDX-License-Identifier: Apache-2.0
import React from 'react';
import { createRoot } from 'react-dom/client';
import '@arco-design/web-react/dist/css/arco.css';
import 'virtual:uno.css';
import './styles.css';
import './visuals/visuals.css';
import './i18n';
import { App } from './App';
import { FeedbackProvider } from './feedback';
createRoot(document.getElementById('root')!).render(<React.StrictMode><FeedbackProvider><App /></FeedbackProvider></React.StrictMode>);
