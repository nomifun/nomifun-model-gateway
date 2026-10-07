// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, ConfigProvider, Drawer, Form, Input, Spin } from '@arco-design/web-react';
import { IconApps, IconArrowRight, IconBook, IconCheckCircle, IconCode, IconCommand, IconDashboard, IconDesktop, IconExperiment, IconFile, IconGift, IconIdcard, IconLink, IconLock, IconMenu, IconMoon, IconPoweroff, IconSearch, IconSettings, IconStorage, IconSun, IconSwap, IconUser, IconSafe } from '@arco-design/web-react/icon';
import enUS from '@arco-design/web-react/es/locale/en-US';
import zhCN from '@arco-design/web-react/es/locale/zh-CN';
import { useTranslation } from 'react-i18next';
import { request, safeHTTPS, session, signOut, text } from './api';
import type { Row } from './api';
import { AdminPages } from './AdminPages';
import { UserPages } from './UserPages';
import { ErrorBox } from './components';
import { CommandPalette } from './visuals/command-palette';

const userNav = [{ id: 'overview', icon: IconDashboard }, { id: 'catalog', icon: IconApps }, { id: 'keys', icon: IconLock }, { id: 'usage', icon: IconExperiment }, { id: 'plans', icon: IconGift }, { id: 'orders', icon: IconSafe }];
const adminGroups = [
  { title: 'platformGroup', items: [{ id: 'channels', icon: IconLink }, { id: 'models', icon: IconApps }, { id: 'users', icon: IconUser }] },
  { title: 'billingGroup', items: [{ id: 'adminplans', icon: IconGift }, { id: 'adminorders', icon: IconSafe }, { id: 'redeemcodes', icon: IconIdcard }] },
  { title: 'operationsGroup', items: [{ id: 'settings', icon: IconSettings }, { id: 'paymentconfig', icon: IconSwap }, { id: 'logs', icon: IconFile }, { id: 'ledger', icon: IconBook }, { id: 'reservations', icon: IconStorage }] }
];
const adminNav = adminGroups.flatMap(group => group.items);
const allNav = [...userNav, ...adminNav];
function locationPage() { const id = window.location.hash.slice(1); return allNav.some(item => item.id === id) ? id : 'overview'; }
function Brand({ name }: { name: string }) { return <div className="brand"><div className="brand-icon" aria-hidden="true"><IconCommand /></div><div className="brand-copy"><strong>{name}</strong><small>MODEL GATEWAY</small></div></div>; }
function Footer({ config }: { config: Row }) {
  const { t } = useTranslation(); const operator = (config.meta as Row | undefined)?.operator as Row | undefined;
  return <footer className="footer"><span>{t('community')}</span><div className="footer-links">{[['homepage_url', 'homepage'], ['terms_url', 'terms'], ['privacy_url', 'privacy'], ['purchase_url', 'purchase']].map(([key, label]) => safeHTTPS(operator?.[key]) && <a key={key} href={safeHTTPS(operator?.[key])} target="_blank" rel="noopener noreferrer">{t(label)}</a>)}</div></footer>;
}
export function App() {
  const { t, i18n } = useTranslation();
  const [config, setConfig] = useState<Row>({}); const [user, setUser] = useState<Row>(); const [busy, setBusy] = useState(true); const [error, setError] = useState('');
  const [page, setPage] = useState(locationPage); const [register, setRegister] = useState(false); const [email, setEmail] = useState(''); const [password, setPassword] = useState(''); const [name, setName] = useState(''); const [loginBusy, setLoginBusy] = useState(false);
  const [mobileNav, setMobileNav] = useState(false); const [commandOpen, setCommandOpen] = useState(false); const [query, setQuery] = useState(''); const [theme, setTheme] = useState<'light' | 'dark'>('light');
  const headingRef = useRef<HTMLDivElement>(null); const lastUserPage = useRef('overview'); const lastAdminPage = useRef('channels');
  const loadConfig = useCallback(async () => { setConfig(await request('/config')); }, []);
  const bootstrap = useCallback(async () => { setBusy(true); setError(''); try { await loadConfig(); if (session.get()) { const me = await request('/me'); setUser(me.user as Row); } } catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { setBusy(false); } }, [loadConfig]);
  useEffect(() => { void bootstrap(); const expire = () => { setUser(undefined); setMobileNav(false); setCommandOpen(false); setPassword(''); setError('sessionExpired'); }; window.addEventListener('nmg-session-expired', expire); return () => window.removeEventListener('nmg-session-expired', expire); }, [bootstrap]);
  const navigate = useCallback((id: string) => { if (!allNav.some(item => item.id === id)) return; window.location.hash = id; setPage(id); setMobileNav(false); setCommandOpen(false); setQuery(''); window.scrollTo({ top: 0 }); }, []);
  useEffect(() => { const update = () => { setPage(locationPage()); setMobileNav(false); }; window.addEventListener('hashchange', update); return () => window.removeEventListener('hashchange', update); }, []);
  useEffect(() => { if (user && !user.admin && adminNav.some(item => item.id === page)) navigate('overview'); if (userNav.some(item => item.id === page)) lastUserPage.current = page; else lastAdminPage.current = page; }, [page, user, navigate]);
  useEffect(() => { if (user) headingRef.current?.focus({ preventScroll: true }); }, [page, user]);
  useEffect(() => { const key = (event: KeyboardEvent) => { if (user && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    // A page change must never discard an editor or a one-time secret dialog.
    const overlayOpen = Array.from(document.querySelectorAll('.arco-modal, .arco-drawer')).some(element => element.getClientRects().length > 0);
    if (!commandOpen && overlayOpen) return;
    setCommandOpen(value => !value); setQuery('');
  } }; window.addEventListener('keydown', key); return () => window.removeEventListener('keydown', key); }, [user, commandOpen]);
  const login = async () => { if (loginBusy) return; setLoginBusy(true); setError(''); try { const result = await request(register ? '/register' : '/login', 'POST', { email: email.trim(), password, ...(register ? { name: name.trim() } : {}) }); if (!result.session_token || !result.user) throw new Error('invalid_response'); session.set(String(result.session_token)); setUser(result.user as Row); setPassword(''); navigate('overview'); } catch (error) { setError(error instanceof Error ? error.message : 'failed'); } finally { setLoginBusy(false); } };
  const logout = async () => { try { await signOut(); setError(''); } catch { setError('logoutFailed'); } finally { setUser(undefined); setMobileNav(false); setCommandOpen(false); navigate('overview'); } };
  useEffect(() => { document.documentElement.lang = i18n.language; }, [i18n.language]);
  useEffect(() => { document.documentElement.dataset.theme = theme; if (theme === 'dark') document.body.setAttribute('arco-theme', 'dark'); else document.body.removeAttribute('arco-theme'); }, [theme]);
  const operator = (config.meta as Row | undefined)?.operator as Row | undefined;
  const operatorName = text(operator?.name ?? t('app'));
  const adminMode = adminNav.some(item => item.id === page);
  const availableNav = user?.admin ? allNav : userNav;
  const matches = availableNav.filter(item => `${t(item.id)} ${item.id}`.toLowerCase().includes(query.trim().toLowerCase()));
  const language = <select className="language-select" aria-label={t('language')} value={i18n.language} onChange={event => { const value = event.target.value; localStorage.setItem('nmg.console.language', value); void i18n.changeLanguage(value); }}><option value="zh-CN">简体中文</option><option value="en-US">English</option></select>;
  const themeButton = <Button type="text" className="icon-button" aria-label={t(theme === 'light' ? 'darkMode' : 'lightMode')} title={t(theme === 'light' ? 'darkMode' : 'lightMode')} icon={theme === 'light' ? <IconMoon /> : <IconSun />} onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')} />;
  const navItems = (entries: typeof userNav) => entries.map(({ id, icon: Icon }) => <a className={'nav-button ' + (page === id ? 'active' : '')} key={id} href={'#' + id} aria-current={page === id ? 'page' : undefined} onClick={event => { event.preventDefault(); navigate(id); }}><Icon aria-hidden="true" /><span>{t(id)}</span>{page === id && <span className="nav-active-dot" />}</a>);
  const navigation = <><Brand name={operatorName} />{user?.admin && <div className="workspace-switch" role="group" aria-label={t('switchWorkspace')}><button className={!adminMode ? 'selected' : ''} onClick={() => navigate(lastUserPage.current)}>{t('personalWorkspace')}</button><button className={adminMode ? 'selected' : ''} onClick={() => navigate(lastAdminPage.current)}>{t('adminWorkspace')}</button></div>}<nav aria-label={t(adminMode ? 'administration' : 'workspace')}>{adminMode ? adminGroups.map(group => <div key={group.title}><div className="nav-group">{t(group.title)}</div>{navItems(group.items)}</div>) : <><div className="nav-group">{t('workspace')}</div>{navItems(userNav)}</>}</nav><div className="sidebar-bottom"><div className="operator-label"><span className="status-dot" /><span>{t('independentOperator')}</span></div><div className="sidebar-account"><span className="avatar" aria-hidden="true">{String(user?.name ?? user?.email ?? 'U').slice(0, 1).toUpperCase()}</span><div><strong>{text(user?.name)}</strong><small>{t(user?.admin ? 'admin' : 'account')}</small></div><Button type="text" className="icon-button" aria-label={t('logout')} title={t('logout')} icon={<IconPoweroff />} onClick={() => void logout()} /></div></div></>;
  return <ConfigProvider locale={i18n.language === 'zh-CN' ? zhCN : enUS} componentConfig={{ Modal: { maskClosable: false } }}>
    {busy ? <div className="boot-loading" role="status"><Spin size={28} /><p>{t('loading')}</p></div> : !user ? <div className="auth">
      <section className="auth-story"><Brand name={operatorName} /><div className="auth-story-content"><div className="eyebrow"><span className="status-dot" />{t('oneConnection')}</div><h1>{t('welcome')}</h1><p className="auth-description">{t('authDescription')}</p><div className="auth-visual" aria-hidden="true"><div className="auth-flow-source"><IconDesktop /><span>NomiFun Desktop</span></div><div className="auth-flow-line" /><div className="auth-flow-gateway"><IconCommand /><span>Model Gateway</span></div><div className="auth-flow-line" /><div className="auth-flow-targets"><span><IconCode /> OpenAI</span><span><IconCode /> Anthropic</span><span><IconCode /> Gemini</span></div></div><div className="auth-benefits"><div><IconCode /><span>{t('nativeProtocols')}</span></div><div><IconExperiment /><span>{t('clearUsage')}</span></div><div><IconLock /><span>{t('scopedAccess')}</span></div></div></div><div className="auth-story-note"><IconCheckCircle />{t('independentOperator')}</div></section>
      <section className="auth-form"><div className="auth-tools">{language}{themeButton}</div><div className="auth-form-inner"><div className="auth-form-mark"><IconCommand /></div><div className="eyebrow">{t('accountAccess')}</div><h2>{t(register ? 'register' : 'loginWelcome')}</h2><p className="muted">{t('loginHint')}</p><ErrorBox error={error} retry={!Object.keys(config).length ? bootstrap : undefined} />{register && !config.registration_enabled && <Alert content={t('registrationDisabled')} type="warning" />}<Form layout="vertical" onSubmit={login}>
        {register && <Form.Item label={<label htmlFor="auth-name">{t('name')}</label>}><Input id="auth-name" value={name} onChange={setName} autoComplete="name" placeholder={t('namePlaceholder')} /></Form.Item>}
        <Form.Item label={<label htmlFor="auth-email">{t('email')}</label>} required><Input id="auth-email" value={email} onChange={setEmail} autoComplete="email" type="email" placeholder="you@example.com" required /></Form.Item>
        <Form.Item label={<label htmlFor="auth-password">{t('password')}</label>} required><Input.Password id="auth-password" value={password} onChange={setPassword} autoComplete={register ? 'new-password' : 'current-password'} placeholder={t('passwordPlaceholder')} required /></Form.Item>
        <Button long type="primary" htmlType="submit" loading={loginBusy} disabled={!email.trim() || !password || register && !config.registration_enabled}>{t(register ? 'register' : 'login')}<IconArrowRight /></Button></Form><div className="auth-switch"><span>{t(register ? 'alreadyRegistered' : 'newHere')}</span><Button type="text" onClick={() => { setRegister(!register); setPassword(''); setError(''); }}>{t(register ? 'login' : 'register')}</Button></div><Footer config={config} /></div></section></div>
      : <div className="shell"><a className="skip-link" href="#main-content" onClick={event => { event.preventDefault(); headingRef.current?.focus(); }} >{t('skipContent')}</a><aside className="sidebar">{navigation}</aside><Drawer title={t('navigation')} visible={mobileNav} onCancel={() => setMobileNav(false)} placement="left" width={280} footer={null} unmountOnExit className="mobile-nav"><div className="mobile-nav-content">{navigation}</div></Drawer>
        <main className="workspace" id="main-content"><header className="topbar"><div className="topbar-location"><Button type="text" className="mobile-menu icon-button" aria-label={t('openNavigation')} icon={<IconMenu />} onClick={() => setMobileNav(true)} /><span className="breadcrumb-root">{t(adminMode ? 'administration' : 'workspace')}</span><span className="breadcrumb-divider">/</span><span className="topbar-title">{t(page)}</span></div><div className="topbar-tools"><button className="nav-search" onClick={() => { setQuery(''); setCommandOpen(true); }} aria-label={t('searchPages')}><IconSearch /><span>{t('searchPages')}</span><kbd>{/Mac/.test(navigator.platform) ? '⌘ K' : 'Ctrl K'}</kbd></button>{language}{themeButton}</div></header><div className="page" ref={headingRef} tabIndex={-1}>{userNav.some(item => item.id === page) ? <UserPages key={page} page={page} config={config} user={user} onNavigate={navigate} /> : user.admin ? <AdminPages key={page} page={page} onConfigChanged={loadConfig} /> : null}</div><Footer config={config} /></main>
        {commandOpen && <CommandPalette items={matches.map(({ id, icon: Icon }) => ({ id, label: t(id), group: t(userNav.some(item => item.id === id) ? 'workspace' : 'administration'), icon: <Icon /> }))} query={query} onQueryChange={setQuery} onSelect={navigate} onClose={() => setCommandOpen(false)} title={t('searchPages')} placeholder={t('searchPagesPlaceholder')} noResultsLabel={t('noSearchResults')} navigateLabel={t('navigation')} selectLabel={t('commandSelect')} closeLabel={t('closeSearch')} />}
      </div>}
  </ConfigProvider>;
}
