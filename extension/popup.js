/* global chrome, RG, ROUTE_FA */
const $ = s => document.querySelector(s);
const root = $('#root');
const FA = '۰۱۲۳۴۵۶۷۸۹';
const fa = v => String(v).replace(/\d/g, d => FA[d]);
const esc = s => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
function speed(n) {
  n = Number(n) || 0;
  const u = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  while (n >= 1024 && i < 3) { n /= 1024; i++; }
  return fa(n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)) + ' ' + u[i] + '/s';
}
function toast(text, err) {
  document.querySelectorAll('.toast').forEach(t => t.remove());
  const t = document.createElement('div');
  t.className = 'toast' + (err ? ' err' : '');
  t.textContent = text;
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 3500);
}

const P = { st: null, site: null, host: '', tab: null, servers: null, cfg: null, busy: false, probing: false };

const GATE = `<svg viewBox="0 0 64 72" aria-hidden="true">
  <path class="fill" d="M8 70V36C8 20 18 9 32 3c14 6 24 17 24 33v34z"/>
  <path class="arch" d="M8 70V36C8 20 18 9 32 3c14 6 24 17 24 33v34"/>
  <path class="road" d="M32 71V36"/></svg>`;

function stateText(st) {
  return { connected: 'متصل', connecting: 'در حال اتصال…', disconnecting: 'در حال قطع…', error: 'خطا در اتصال', idle: 'قطع' }[st.state] || st.state;
}

function renderOffline(e) {
  if (e.code === 'unpaired') {
    root.innerHTML = `<div class="state-box"><b>اتصال به برنامه رهگذر</b>
      <p>یک بار اجازه بدهید این مرورگر رهگذر را کنترل کند. بعد از زدن دکمه، داخل برنامه رهگذر پیغام تأیید می‌آید.</p>
      <button class="btn primary wide" id="pairBtn">اتصال به رهگذر</button><p class="hint" id="pairMsg"></p></div>`;
    $('#pairBtn').onclick = async () => {
      $('#pairBtn').disabled = true;
      $('#pairMsg').textContent = 'منتظر تأیید در برنامه رهگذر…';
      try {
        const ok = await RG.pair(s => { if (s === 'pending') $('#pairMsg').textContent = 'منتظر تأیید در برنامه رهگذر…'; });
        if (ok) { chrome.runtime.sendMessage({ type: 'refresh' }); load(); }
        else { $('#pairMsg').textContent = 'درخواست رد شد یا زمانش تمام شد.'; $('#pairBtn').disabled = false; }
      } catch (err) { $('#pairMsg').textContent = err.message; $('#pairBtn').disabled = false; }
    };
    return;
  }
  root.innerHTML = `<div class="state-box"><b>برنامه رهگذر باز نیست</b>
    <p>${esc(e.message)}<br>اگر برنامه باز است، پورت ارتباط را در تنظیمات اکستنشن با تنظیمات برنامه یکی کنید.</p>
    <button class="btn wide" id="retry">تلاش دوباره</button></div>`;
  $('#retry').onclick = load;
}

const flag = cc => /^[a-z]{2}$/.test(cc || '') ? `<img class="flag" src="flags/${cc}.png" alt="${cc.toUpperCase()}" title="${cc.toUpperCase()}">` : '<span class="flag none"></span>';
const SPARK = '<svg class="spark" viewBox="0 0 24 24"><path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5L18 18M6 18l2.5-2.5M15.5 8.5L18 6"/></svg>';

function serverPicker() {
  const sv = P.servers;
  const sel = (sv && sv.selectedId) || 'auto';
  const cur = sv && sv.servers.find(s => s.id === sel);
  const label = cur ? `${flag(cur.country)}<span class="nm">${esc(cur.name)}</span>` : `${SPARK}<span class="nm">انتخاب هوشمند</span>`;
  let list = '';
  if (P.pickerOpen && sv) {
    const items = [`<button data-sid="auto" aria-pressed="${sel === 'auto'}">${SPARK}<span class="nm">انتخاب هوشمند (بهترین سرور)</span></button>`];
    for (const s of sv.servers.slice(0, 120)) {
      const lat = s.latency ? `<span class="lat">${fa(Math.round(s.latency))}</span>` : '';
      items.push(`<button data-sid="${esc(s.id)}" aria-pressed="${sel === s.id}">${flag(s.country)}<span class="nm">${esc(s.name)}</span>${lat}</button>`);
    }
    list = `<div class="plist" id="plist">${items.join('')}</div>`;
  }
  return `<button class="pbtn" id="pbtn" aria-expanded="${!!P.pickerOpen}">${label}<svg class="chev" viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"/></svg></button>${list}`;
}

function siteHTML() {
  if (!P.host) return `<div class="site"><div class="nosite">این صفحه یک سایت معمولی نیست؛ برای تعیین مسیر، روی یک سایت باشید.</div></div>`;
  const si = P.site;
  if (!si) return `<div class="site"><div class="nosite">…</div></div>`;
  const fav = P.tab && P.tab.favIconUrl && /^https?:|^data:/.test(P.tab.favIconUrl) ? `<img src="${esc(P.tab.favIconUrl)}" alt="">` : '';
  const rule = si.rule || 'auto';
  const btn = (a, label) => `<button data-a="${a}" aria-pressed="${rule === a}">${label}</button>`;
  return `<div class="site">
    <div class="site-head">${fav}<span class="host">${esc(P.host)}</span><span class="route ${si.route}">${ROUTE_FA[si.route] || ''}</span></div>
    <div class="why">${esc(si.why || '')}${si.ruleOn && si.ruleOn !== P.host ? ' <span class="ltr">(' + esc(si.ruleOn) + ')</span>' : ''}</div>
    <div class="seg" role="group" aria-label="مسیر این سایت">${btn('auto', 'خودکار')}${btn('proxy', 'تونل')}${btn('direct', 'مستقیم')}${btn('block', 'مسدود')}</div>
    <div class="site-foot">
      <label><input type="checkbox" id="scopeHost" ${P.cfg.siteScope === 'host' ? 'checked' : ''}>فقط <span class="ltr">${esc(P.host)}</span></label>
      <span class="grow"></span>
      <button class="link-btn" id="probe" ${P.probing ? 'disabled' : ''}>${P.probing ? 'در حال تست…' : 'تست مستقیم/تونل'}</button>
    </div></div>`;
}

function render() {
  const st = P.st;
  const err = st.state === 'error' && st.error ? `<div class="err">${esc(st.error)}</div>` : '';
  const srv = st.state === 'connected' ? `${st.country ? flag(st.country) + ' ' : ''}${esc(st.profileName || '')}${st.latency > 0 ? ' · ' + fa(st.latency) + 'ms' : ''}` : (st.message ? esc(st.message) : (st.enginesOk ? 'برای اتصال روی دروازه کلیک کنید' : 'موتورها نصب نیستند؛ برنامه را باز کنید'));
  root.innerHTML = `
    ${P.updateHint ? `<div class="notice">نسخه ${esc(P.updateHint)} اکستنشن کنار برنامه آماده است ولی این اکستنشن از پوشه دیگری نصب شده. در chrome://extensions آن را حذف کنید و پوشه <b>extension</b> کنار Rahgozar.exe را Load unpacked کنید تا از این به بعد خودکار به‌روز شود.</div>` : ''}
    <div class="conn">
      <button class="gate" id="gate" data-state="${esc(st.state)}" title="اتصال / قطع (Alt+Shift+R)">${GATE}</button>
      <div class="conn-txt"><b>${stateText(st)}</b><div class="srv">${srv}</div>${err}
        ${st.state === 'connected' ? `<div class="speeds"><span>↓ <b id="sd">${speed(st.downSpeed)}</b></span><span>↑ <b id="su">${speed(st.upSpeed)}</b></span></div>` : ''}
      </div>
    </div>
    ${siteHTML()}
    <div class="block">
      <div class="picker-wrap">${serverPicker()}</div>
      <div class="row"><span>حالت اتصال</span><div class="mini-seg" id="mode">
        <button data-m="proxy" aria-pressed="${st.mode === 'proxy'}">پروکسی</button><button data-m="tun" aria-pressed="${st.mode === 'tun'}">TUN</button></div></div>
      <div class="row"><span>مسیریابی</span><select class="sel" id="routing">
        ${[['smart', 'هوشمند'], ['rules', 'فقط قوانین'], ['global', 'همه از تونل'], ['direct', 'مستقیم']].map(([v, t]) => `<option value="${v}" ${st.routingMode === v ? 'selected' : ''}>${t}</option>`).join('')}
      </select></div>
      <div class="row"><span>فقط این مرورگر از تونل (بدون پروکسی ویندوز)</span><label class="switch"><input type="checkbox" id="bproxy" ${P.cfg.browserProxy ? 'checked' : ''}><i></i></label></div>
      <div class="row"><span></span><button class="link-btn" id="testAll">تست سرعت همه سرورها</button></div>
    </div>`;
  wire();
}

function wire() {
  $('#gate').onclick = async () => {
    if (P.busy) return;
    P.busy = true;
    try { P.st = await RG.toggle(); render(); } catch (e) { toast(e.message, true); }
    P.busy = false;
    chrome.runtime.sendMessage({ type: 'refresh' });
  };
  $('#pbtn').onclick = () => { P.pickerOpen = !P.pickerOpen; render(); const l = $('#plist'); if (l) { const a = l.querySelector('[aria-pressed="true"]'); if (a) a.scrollIntoView({ block: 'nearest' }); } };
  document.querySelectorAll('#plist [data-sid]').forEach(b => b.onclick = async () => {
    P.pickerOpen = false;
    try { P.st = await RG.select(b.dataset.sid); P.servers.selectedId = b.dataset.sid; toast('سرور انتخاب شد'); } catch (err) { toast(err.message, true); }
    render();
  });
  document.querySelectorAll('#mode button').forEach(b => b.onclick = async () => {
    try { P.st = await RG.mode(b.dataset.m); render(); } catch (e) { toast(e.message, true); }
  });
  $('#routing').onchange = async e => {
    try { P.st = await RG.routing(e.target.value); await loadSite(); render(); chrome.runtime.sendMessage({ type: 'refresh' }); } catch (err) { toast(err.message, true); }
  };
  $('#bproxy').onchange = async e => {
    await RG.setCfg({ browserProxy: e.target.checked });
    P.cfg.browserProxy = e.target.checked;
    chrome.runtime.sendMessage({ type: 'syncProxy' });
    toast(e.target.checked ? 'این مرورگر هنگام اتصال از تونل رهگذر عبور می‌کند' : 'پروکسی مرورگر برداشته شد');
  };
  $('#testAll').onclick = async () => {
    try { const r = await RG.testAll(); toast('تست ' + fa(r.started) + ' سرور شروع شد؛ نتیجه در برنامه و همین فهرست'); setTimeout(loadServers, 8000); } catch (e) { toast(e.message, true); }
  };
  document.querySelectorAll('.seg button[data-a]').forEach(b => b.onclick = async () => {
    try {
      P.site = await RG.setSite(P.host, b.dataset.a, P.cfg.siteScope);
      render();
      chrome.runtime.sendMessage({ type: 'refresh' });
      if (P.tab && b.dataset.a !== 'auto') chrome.tabs.reload(P.tab.id);
    } catch (e) { toast(e.message, true); }
  });
  const sc = $('#scopeHost');
  if (sc) sc.onchange = async e => { P.cfg.siteScope = e.target.checked ? 'host' : 'site'; await RG.setCfg({ siteScope: P.cfg.siteScope }); };
  const pr = $('#probe');
  if (pr) pr.onclick = async () => {
    P.probing = true; render();
    try { P.site = await RG.probe(P.host); toast((P.site.learned === 'proxy' ? 'نیاز به تونل: ' : P.site.learned === 'direct' ? 'مستقیم باز می‌شود: ' : 'نتیجه: ') + (P.site.reason || '')); }
    catch (e) { toast(e.message, true); }
    P.probing = false; render();
    chrome.runtime.sendMessage({ type: 'refresh' });
  };
}

async function loadSite() {
  if (!P.host) return;
  try { P.site = await RG.site(P.host); } catch (e) { P.site = null; }
}
async function loadServers() {
  try { P.servers = await RG.servers(); if (P.st) render(); } catch (e) { /* ignore */ }
}

let poll = null;
async function load() {
  chrome.runtime.sendMessage({ type: 'checkUpdate' });
  P.cfg = await RG.cfg();
  const { updateHint } = await chrome.storage.local.get('updateHint');
  P.updateHint = updateHint && updateHint !== chrome.runtime.getManifest().version ? updateHint : '';
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  P.tab = tab || null;
  P.host = tab ? RG.hostOf(tab.url) : '';
  try {
    P.st = await RG.status();
  } catch (e) { renderOffline(e); return; }
  await loadSite();
  render();
  loadServers();
  clearInterval(poll);
  poll = setInterval(async () => {
    if (P.busy || P.probing || P.pickerOpen || document.activeElement && document.activeElement.tagName === 'SELECT') return;
    try {
      const prev = P.st;
      P.st = await RG.status();
      if (prev.state !== P.st.state) await loadSite();
      const key = s => [s.state, s.profileId, s.latency, s.mode, s.routingMode, s.error, s.message, s.selectedId, s.country].join('|');
      if (key(prev) !== key(P.st)) render();
      else {
        const sd = $('#sd'), su = $('#su');
        if (sd) sd.textContent = speed(P.st.downSpeed);
        if (su) su.textContent = speed(P.st.upSpeed);
      }
    } catch (e) { clearInterval(poll); renderOffline(e); }
  }, 1200);
}

$('#openOptions').onclick = () => chrome.runtime.openOptionsPage();
load();
