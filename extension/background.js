/* global chrome, importScripts, RG */
importScripts('lib.js');

// ---------- toolbar badge: shows how the current tab's site is routed ----------
const siteCache = new Map(); // host -> {info, at}
let lastStatus = null;

function setIcon(on) {
  const suffix = on ? '' : '-off';
  chrome.action.setIcon({ path: { 16: `icons/icon16${suffix}.png`, 32: `icons/icon32${suffix}.png` } }).catch(() => {});
}

async function refreshStatus() {
  try {
    lastStatus = await RG.status();
  } catch (e) {
    lastStatus = { state: e.code === 'unpaired' ? 'unpaired' : 'offline' };
  }
  const on = lastStatus.state === 'connected';
  setIcon(on);
  await syncBrowserProxy();
  return lastStatus;
}

// browser-only proxy follows the connection: never leave the browser pointing
// at a dead local port.
async function syncBrowserProxy() {
  const c = await RG.cfg();
  const want = c.browserProxy && lastStatus && lastStatus.state === 'connected';
  try { await RG.applyBrowserProxy(want, lastStatus && lastStatus.proxyPort); } catch (e) { /* ignore */ }
}

async function siteInfo(host, force) {
  const c = siteCache.get(host);
  if (!force && c && Date.now() - c.at < 30000) return c.info;
  const info = await RG.site(host);
  siteCache.set(host, { info, at: Date.now() });
  return info;
}

async function updateBadge(tabId, url) {
  const host = RG.hostOf(url || '');
  const st = lastStatus || await refreshStatus();
  if (!host || st.state === 'offline' || st.state === 'unpaired') {
    chrome.action.setBadgeText({ tabId, text: st.state === 'unpaired' ? '!' : '' });
    if (st.state === 'unpaired') chrome.action.setBadgeBackgroundColor({ tabId, color: '#f2a33a' });
    chrome.action.setTitle({ tabId, title: st.state === 'offline' ? 'رهگذر: برنامه باز نیست' : st.state === 'unpaired' ? 'رهگذر: برای اتصال کلیک کنید' : 'رهگذر' });
    return;
  }
  try {
    const info = await siteInfo(host);
    const on = st.state === 'connected';
    let text = '', color = '#5b728b';
    if (info.route === 'block') { text = '✕'; color = '#e5566b'; }
    else if (!on) { text = ''; }
    else if (info.route === 'proxy') { text = 'T'; color = '#22a594'; }
    else { text = 'D'; color = '#4c7df0'; }
    chrome.action.setBadgeText({ tabId, text });
    chrome.action.setBadgeBackgroundColor({ tabId, color });
    const conn = on ? 'متصل' : 'قطع';
    chrome.action.setTitle({ tabId, title: `رهگذر (${conn})\n${host}: ${ROUTE_FA[info.route] || ''}\n${info.why || ''}` });
  } catch (e) { /* ignore */ }
}

async function updateActive() {
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (tab) updateBadge(tab.id, tab.url);
}

chrome.tabs.onActivated.addListener(({ tabId }) => chrome.tabs.get(tabId, t => !chrome.runtime.lastError && updateBadge(tabId, t.url)));
chrome.tabs.onUpdated.addListener((tabId, ch, tab) => { if (ch.url || ch.status === 'complete') updateBadge(tabId, tab.url); });

chrome.alarms.create('rg-status', { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener(async a => {
  if (a.name !== 'rg-status') return;
  const prev = lastStatus && lastStatus.state;
  await refreshStatus();
  if (prev !== lastStatus.state) updateActive();
});

// ---------- context menus ----------
const MENU = [
  ['proxy', 'این سایت همیشه از تونل برود'],
  ['direct', 'این سایت همیشه مستقیم برود'],
  ['block', 'این سایت مسدود شود'],
  ['auto', 'مسیر این سایت خودکار شود'],
];
function buildMenus() {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({ id: 'rg', title: 'رهگذر', contexts: ['page', 'link', 'frame'] });
    for (const [id, title] of MENU) {
      chrome.contextMenus.create({ id: 'page-' + id, parentId: 'rg', title, contexts: ['page', 'frame'] });
      chrome.contextMenus.create({ id: 'link-' + id, parentId: 'rg', title: title.replace('این سایت', 'سایت این لینک'), contexts: ['link'] });
    }
    chrome.contextMenus.create({ id: 'sep', parentId: 'rg', type: 'separator', contexts: ['page', 'link', 'frame'] });
    chrome.contextMenus.create({ id: 'toggle', parentId: 'rg', title: 'اتصال / قطع رهگذر', contexts: ['page', 'link', 'frame'] });
  });
}
chrome.runtime.onInstalled.addListener(() => {
  buildMenus();
  chrome.runtime.openOptionsPage && RG.cfg().then(c => { if (!c.token) chrome.runtime.openOptionsPage(); });
});
chrome.runtime.onStartup.addListener(() => { buildMenus(); refreshStatus(); });

async function notifyTab(tabId, text) {
  chrome.action.setBadgeText({ tabId, text: '…' });
  setTimeout(() => chrome.tabs.get(tabId, t => !chrome.runtime.lastError && updateBadge(tabId, t.url)), 1200);
  chrome.action.setTitle({ tabId, title: text });
}

async function applySite(host, action, tabId) {
  if (!host) return;
  try {
    const c = await RG.cfg();
    const info = await RG.setSite(host, action, c.siteScope);
    siteCache.delete(host);
    siteCache.set(host, { info, at: Date.now() });
    if (tabId != null) notifyTab(tabId, `${host}: ${RULE_FA[action]}`);
    return info;
  } catch (e) {
    if (tabId != null) { chrome.action.setBadgeText({ tabId, text: '!' }); chrome.action.setTitle({ tabId, title: e.message }); }
  }
}

chrome.contextMenus.onClicked.addListener(async (info, tab) => {
  const id = info.menuItemId;
  if (id === 'toggle') { try { await RG.toggle(); } catch (e) { /* shown in popup */ } setTimeout(refreshStatus, 1500); return; }
  const [kind, action] = String(id).split('-');
  const url = kind === 'link' ? info.linkUrl : (info.frameUrl || info.pageUrl || (tab && tab.url));
  await applySite(RG.hostOf(url), action, tab && tab.id);
  if (kind === 'page' && tab && action !== 'auto') chrome.tabs.reload(tab.id);
});

// ---------- keyboard shortcuts ----------
chrome.commands.onCommand.addListener(async cmd => {
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (cmd === 'toggle-connection') {
    try { await RG.toggle(); } catch (e) { /* ignore */ }
    setTimeout(async () => { await refreshStatus(); updateActive(); }, 2500);
  }
  if (cmd === 'toggle-site' && tab) {
    const host = RG.hostOf(tab.url);
    if (!host) return;
    const info = await siteInfo(host, true).catch(() => null);
    if (!info) return;
    await applySite(host, info.route === 'proxy' ? 'direct' : 'proxy', tab.id);
    chrome.tabs.reload(tab.id);
  }
});

// ---------- messages from popup ----------
chrome.runtime.onMessage.addListener((msg, sender, reply) => {
  (async () => {
    if (msg.type === 'refresh') { siteCache.clear(); await refreshStatus(); await updateActive(); reply({ ok: true }); }
    else if (msg.type === 'syncProxy') { await refreshStatus(); reply({ ok: true }); }
    else reply({});
  })();
  return true;
});

refreshStatus().then(updateActive);
