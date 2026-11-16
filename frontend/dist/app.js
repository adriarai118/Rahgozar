// Application shell: state, navigation, backend events.
const S = {
  page: 'home',
  st: null,              // AppState from backend
  speedDown: [], speedUp: [],
  traffic: null,
  tests: {},             // id -> {ok, ms, error, running}
  testing: false,
  engines: null,
  learning: null,
  logs: [],
  group: 'all',
  query: '',
  selected: new Set(),
  logTab: 'logs',
  appUpdate: null,
};

const NAV = [
  ['home', 'خانه', 'home'],
  ['servers', 'سرورها', 'servers'],
  ['routing', 'مسیریابی', 'routing'],
  ['learn', 'یادگیری', 'learn'],
  ['engines', 'موتورها', 'engines'],
  ['settings', 'تنظیمات', 'settings'],
  ['logs', 'گزارش‌ها', 'logs'],
];

function renderRail() {
  const rail = document.getElementById('rail');
  const st = (S.st && S.st.status) || {};
  const dot = st.state === 'connected' ? 'on' : st.state === 'error' ? 'err' : st.state === 'connecting' ? 'warn' : '';
  const engBadge = S.engines && S.engines.some(e => e.hasUpdate) ? '<span class="badge warn">جدید</span>' : '';
  rail.innerHTML = `
    <div class="rail-brand">
      <svg viewBox="0 0 32 32" aria-hidden="true"><path d="M5 30V15C5 8 10 3 16 1c6 2 11 7 11 14v15" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round"/><path d="M16 30V14" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-dasharray="3 3.5"/></svg>
      <div><b>رهگذر</b><small class="ltr">v${esc(S.st ? S.st.version : '')}</small></div>
    </div>
    ${NAV.map(([id, label, ic]) => `<button class="nav-btn" data-nav="${id}" ${S.page === id ? 'aria-current="page"' : ''}>${icon(ic)}<span>${label}</span>${id === 'engines' ? engBadge : ''}</button>`).join('')}
    <div class="rail-foot">
      <div class="net"><span class="dot ${dot}"></span><span>${esc(netName(st.netLabel))}</span></div>
    </div>`;
}

function netName(label) {
  if (!label) return 'شبکه';
  return label.split('@')[0];
}

function renderTitle() {
  const st = S.st.status;
  const el = document.getElementById('titleStatus');
  const map = { idle: 'قطع', connecting: 'در حال اتصال…', connected: 'متصل', disconnecting: 'در حال قطع…', error: 'خطا' };
  let t = map[st.state] || '';
  if (st.state === 'connected') t = 'متصل به ' + (st.profileName || '');
  el.textContent = t;
}

function navigate(page) {
  S.page = page;
  render();
  document.getElementById('view').scrollTop = 0;
  if (page === 'engines' && !S.engines) loadEngines();
  if (page === 'learn') loadLearning();
  if (page === 'logs') loadLogs();
}

function render() {
  renderRail();
  if (!S.st) return;
  renderTitle();
  const view = document.getElementById('view');
  const fn = Pages[S.page] || Pages.home;
  view.innerHTML = fn();
  if (Pages[S.page + 'Mount']) Pages[S.page + 'Mount'](view);
}

async function refresh() {
  S.st = await API.call('GetState');
  document.documentElement.dataset.theme = S.st.settings.theme === 'light' ? 'light' : 'dark';
  render();
}

async function refreshProfiles() {
  S.st.profiles = await API.call('GetProfiles');
  if (S.page === 'servers' || S.page === 'home') render();
}

async function loadEngines() {
  S.engines = await API.call('ListEngines');
  if (S.page === 'engines') render(); else renderRail();
}
async function loadLearning() {
  S.learning = await API.call('GetLearning');
  if (S.page === 'learn') render();
}
async function loadLogs() {
  S.logs = (await API.call('GetLogs')) || [];
  if (S.page === 'logs') render();
}

async function saveSettings(patch) {
  const s = Object.assign({}, S.st.settings, patch);
  try {
    await API.call('SaveSettings', s);
    S.st.settings = s;
    document.documentElement.dataset.theme = s.theme === 'light' ? 'light' : 'dark';
  } catch (e) { toast(errText(e), 'error'); }
}

async function needAdmin() {
  const ok = await confirmBox('حالت TUN به دسترسی مدیر نیاز دارد',
    'برای ساختن کارت شبکه مجازی، رهگذر باید با دسترسی Administrator اجرا شود. برنامه بسته و دوباره با دسترسی مدیر باز می‌شود.',
    'اجرای مجدد با دسترسی مدیر');
  if (ok) API.call('RelaunchAdmin').catch(e => toast(errText(e), 'error'));
}

async function toggleConnect() {
  const st = S.st.status.state;
  try {
    if (st === 'connected' || st === 'connecting') await API.call('Disconnect');
    else {
      if (!S.st.enginesOk) { toast('ابتدا موتور sing-box را از بخش «موتورها» نصب کنید', 'warn'); navigate('engines'); return; }
      await API.call('Connect');
    }
  } catch (e) {
    if (errText(e) === 'NEED_ADMIN') needAdmin();
    else toast(errText(e), 'error');
  }
}

// ---------- global events ----------
document.addEventListener('click', e => {
  const nav = e.target.closest('[data-nav]');
  if (nav) { navigate(nav.dataset.nav); return; }
  const w = e.target.closest('.wbtn');
  if (w) {
    const a = w.dataset.act;
    if (a === 'min') API.call('WindowMinimise');
    if (a === 'max') API.call('WindowToggleMaximise');
    if (a === 'quit') API.call('Quit');
    if (a === 'theme') saveSettings({ theme: S.st.settings.theme === 'light' ? 'dark' : 'light' }).then(() => S.page === 'home' && render());
  }
});

API.on('status', st => {
  const prev = S.st.status.state;
  S.st.status = st;
  if (st.state !== 'connected') { if (prev === 'connected') { S.speedDown = []; S.speedUp = []; } }
  if (st.state === 'error' && st.error === 'NEED_ADMIN') { st.error = 'حالت TUN به دسترسی مدیر نیاز دارد'; needAdmin(); }
  renderRail(); renderTitle();
  if (S.page === 'home') Pages.homeUpdate(); else if (S.page === 'servers') render();
});
API.on('traffic', t => {
  S.traffic = t;
  S.speedDown.unshift(t.down); S.speedUp.unshift(t.up);
  if (S.speedDown.length > 90) { S.speedDown.length = 90; S.speedUp.length = 90; }
  if (S.page === 'home') Pages.homeTraffic();
  if (S.page === 'logs' && S.logTab === 'conns') Pages.connsUpdate();
});
API.on('test', r => {
  S.tests[r.id] = r;
  if (S.page === 'servers') Pages.serverRowUpdate(r.id);
});
API.on('testStart', d => { S.testing = true; (d.ids || []).forEach(id => S.tests[id] = { running: true }); if (S.page === 'servers') render(); });
API.on('testDone', () => { S.testing = false; refreshProfiles(); });
API.on('toast', t => toast(t.text, t.kind));
API.on('log', lines => {
  S.logs.push(...lines);
  if (S.logs.length > 3000) S.logs.splice(0, S.logs.length - 3000);
  if (S.page === 'logs' && S.logTab === 'logs') Pages.logsAppend(lines);
});
API.on('engine', es => {
  if (!S.engines) return;
  const i = S.engines.findIndex(e => e.id === es.id);
  if (i >= 0) S.engines[i] = es;
  if (S.page === 'engines') Pages.engineCardUpdate(es);
});
API.on('engines', async () => { await loadEngines(); S.st = await API.call('GetState'); renderRail(); });
API.on('subs', async () => { S.st = await API.call('GetState'); if (S.page === 'servers') render(); });
API.on('profiles', refreshProfiles);
API.on('domain', () => { if (S.page === 'learn') loadLearning(); });
API.on('appUpdate', u => { S.appUpdate = u; toast('نسخه جدید رهگذر (' + u.latest + ') آماده است — بخش موتورها', 'info'); renderRail(); });
API.on('appUpdateProgress', p => { const el = document.getElementById('appUpBar'); if (el) el.style.width = Math.round(p * 100) + '%'; });

window.addEventListener('resize', () => { if (S.page === 'home') Pages.homeTraffic(); });

renderRail();
refresh().then(() => { loadEngines(); }).catch(e => {
  document.getElementById('view').innerHTML = `<div class="empty"><b>ارتباط با هسته برنامه برقرار نشد</b><span class="ltr" style="user-select:text">${esc(errText(e))}</span><br><button class="btn primary" onclick="location.reload()">تلاش دوباره</button></div>`;
});
