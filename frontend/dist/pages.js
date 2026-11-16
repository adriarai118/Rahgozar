// Page renderers.
const Pages = {};

function profileById(id) { return (S.st.profiles || []).find(p => p.id === id); }
function groupName(g) {
  if (!g || g === 'manual') return 'دستی';
  const s = (S.st.subs || []).find(x => x.id === g);
  return s ? s.name : 'اشتراک';
}

// =====================================================================
// HOME
// =====================================================================
const GATE_SVG = `
<svg viewBox="0 0 270 318" aria-hidden="true">
  <defs>
    <radialGradient id="gateGlow" cx="50%" cy="72%" r="70%">
      <stop offset="0" stop-color="currentColor" stop-opacity=".34"/>
      <stop offset=".65" stop-color="currentColor" stop-opacity=".08"/>
      <stop offset="1" stop-color="currentColor" stop-opacity="0"/>
    </radialGradient>
  </defs>
  <path class="arch-fill" d="M38 300V150C38 82 82 36 135 14c53 22 97 68 97 136v150z"/>
  <path class="arch" d="M38 300V150C38 82 82 36 135 14c53 22 97 68 97 136v150"/>
  <path class="arch-inner" d="M58 300V156C58 100 92 62 135 42c43 20 77 58 77 114v144"/>
  <path class="road" d="M135 312V196"/>
  <path class="ground" d="M10 302h250"/>
</svg>`;

function gateLabel(st) {
  switch (st.state) {
    case 'connected': return ['متصل', 'برای قطع، کلیک کنید'];
    case 'connecting': return ['در حال اتصال', st.message || ''];
    case 'disconnecting': return ['در حال قطع', ''];
    case 'error': return ['اتصال', 'دوباره تلاش کنید'];
    default: return ['اتصال', 'برای عبور، کلیک کنید'];
  }
}

function currentPickInfo() {
  const set = S.st.settings, st = S.st.status;
  if (st.state === 'connected' && st.profileId) {
    const p = profileById(st.profileId);
    if (p) return { p, auto: set.selectedId === 'auto' };
  }
  if (set.selectedId === 'auto' || !set.selectedId) return { auto: true };
  return { p: profileById(set.selectedId), auto: false };
}

Pages.home = function () {
  const st = S.st.status, set = S.st.settings;
  const [big, small] = gateLabel(st);
  const info = currentPickInfo();
  let pick;
  if (info.p) {
    const p = info.p;
    pick = `<span class="proto">${p.country ? flag(p.country, 'lg') : esc(PROTO_LABEL[p.type] || p.type)}</span>
      <span class="who"><b>${esc(p.name)}</b><small>${info.auto ? 'انتخاب هوشمند · ' : ''}${esc(PROTO_LABEL[p.type] || p.type)} ${esc(protoSub(p))}</small></span>`;
  } else {
    pick = `<span class="proto">${icon('spark')}</span><span class="who"><b>انتخاب هوشمند</b><small>بهترین سرور برای همین شبکه و همین ساعت</small></span>`;
  }
  const routeName = { smart: 'هوشمند', global: 'همه از تونل', rules: 'فقط قوانین', direct: 'مستقیم' }[set.routingMode];
  return `
  ${!S.st.enginesOk ? `<div class="banner"><span class="grow">موتور sing-box هنوز نصب نشده است. برای شروع، موتورها را دریافت کنید.</span><button class="btn sm primary" data-nav="engines">نصب موتورها</button></div>` : ''}
  <div class="home">
    <section class="gate-wrap">
      <button class="gate" id="gate" data-state="${st.state}" aria-label="${big}">${GATE_SVG}
        <span class="label"><b id="gateBig">${big}</b><span id="gateSmall">${esc(small)}</span></span>
      </button>
      <div class="gate-meta" id="gateMeta">${gateMeta(st)}</div>
      <button class="picker" id="picker">${pick}${icon('chev')}</button>
      <div class="mode-row">
        <div class="seg" role="group" aria-label="حالت اتصال">
          <button data-mode="proxy" aria-pressed="${set.mode === 'proxy'}">${icon('proxy')}${isAndroid() ? 'فقط پروکسی محلی' : 'پروکسی سیستم'}</button>
          <button data-mode="tun" aria-pressed="${set.mode === 'tun'}">${icon('tun')}${isAndroid() ? 'VPN (کل گوشی)' : 'TUN (کل سیستم)'}</button>
        </div>
        <button class="btn sm ghost" data-nav="routing">${icon('routing')}مسیریابی: ${routeName}</button>
      </div>
    </section>
    <section>
      <div class="stats">
        <div class="stat"><span>دانلود</span><b id="sDown">۰<small>B/s</small></b></div>
        <div class="stat"><span>آپلود</span><b id="sUp">۰<small>B/s</small></b></div>
        <div class="stat"><span>تأخیر</span><b id="sPing">—</b></div>
        <div class="stat"><span>مدت اتصال</span><b id="sTime">—</b></div>
      </div>
      <div class="panel chart-panel">
        <div class="chart-head"><span><i style="background:var(--firouzeh)"></i>دانلود <i style="background:var(--saffron);margin-inline-start:12px"></i>آپلود</span><span id="sTotal" class="num"></span></div>
        <canvas class="spark" id="spark"></canvas>
      </div>
      <div class="panel pad facts" id="facts">${factsHTML()}</div>
    </section>
  </div>`;
};

function gateMeta(st) {
  if (st.state === 'error' && st.error) return `<div class="err">${esc(st.error)}</div>`;
  if (st.state === 'connected' && st.message) return `<div class="msg">${esc(st.message)}</div>`;
  if (st.state === 'connected') return `<div class="msg">${st.failovers ? fa(st.failovers) + ' بار تعویض خودکار سرور' : ''}</div>`;
  return '';
}

function factsHTML() {
  const st = S.st.status, set = S.st.settings;
  const rows = [
    ['شبکه', esc(netName(st.netLabel) || '—')],
    ['حالت', set.mode === 'tun' ? (isAndroid() ? 'VPN — همه برنامه‌ها' : 'TUN — همه برنامه‌ها') : (isAndroid() ? 'پروکسی محلی' : 'پروکسی سیستم')],
    ['موتور', esc(st.engine || '—')],
    ['پروتکل', `<span class="ltr">${esc(st.protocol || '—')}</span>`],
    ['پورت محلی', `<span class="ltr num">127.0.0.1:${set.mixedPort}</span>`],
    ['اتصال‌های فعال', `<span class="num" id="sConns">${fa((S.traffic && st.state === 'connected') ? S.traffic.conns : 0)}</span>`],
  ];
  return rows.map(([k, v]) => `<div class="fact"><span>${k}</span><b>${v}</b></div>`).join('');
}

Pages.homeMount = function (v) {
  v.querySelector('#gate').onclick = toggleConnect;
  v.querySelector('#picker').onclick = openPicker;
  v.querySelectorAll('[data-mode]').forEach(b => b.onclick = async () => {
    try {
      await API.call('SetMode', b.dataset.mode);
      S.st.settings.mode = b.dataset.mode;
      render();
    } catch (e) { if (errText(e) === 'NEED_ADMIN') needAdmin(); else toast(errText(e), 'error'); }
  });
  Pages.homeTraffic();
  clearInterval(Pages._clock);
  Pages._clock = setInterval(() => {
    if (S.page !== 'home') return clearInterval(Pages._clock);
    const st = S.st.status, el = document.getElementById('sTime');
    if (el) el.textContent = st.state === 'connected' && st.since ? duration(Date.now() / 1000 - st.since) : '—';
  }, 1000);
};

Pages.homeUpdate = function () {
  const st = S.st.status;
  const gate = document.getElementById('gate');
  if (!gate) return render();
  gate.dataset.state = st.state;
  const [big, small] = gateLabel(st);
  document.getElementById('gateBig').textContent = big;
  document.getElementById('gateSmall').textContent = small;
  document.getElementById('gateMeta').innerHTML = gateMeta(st);
  document.getElementById('facts').innerHTML = factsHTML();
  const info = currentPickInfo();
  if (st.state === 'connected' || st.state === 'idle') {
    const pk = document.getElementById('picker');
    if (pk && info.p && !pk.textContent.includes(info.p.name)) render();
  }
  Pages.homeTraffic();
};

Pages.homeTraffic = function () {
  const st = S.st.status;
  const on = st.state === 'connected';
  const t = on && S.traffic ? S.traffic : { up: 0, down: 0, upTotal: 0, downTotal: 0, conns: 0 };
  const d = speedParts(t.down), u = speedParts(t.up);
  const set = (id, html) => { const el = document.getElementById(id); if (el) el.innerHTML = html; };
  set('sDown', `${d.v}<small class="ltr">${d.u}</small>`);
  set('sUp', `${u.v}<small class="ltr">${u.u}</small>`);
  set('sPing', on && st.latency > 0 ? `${fa(st.latency)}<small>ms</small>` : on && st.latency < 0 ? '<span style="color:var(--anar)">قطع</span>' : '—');
  set('sTotal', on ? `↓ ${bytesStr(t.downTotal)}   ↑ ${bytesStr(t.upTotal)}` : '');
  set('sConns', fa(t.conns || 0));
  const c = document.getElementById('spark');
  if (c) drawSpark(c, S.speedDown, S.speedUp);
};

function openPicker() {
  const set = S.st.settings;
  const ps = S.st.profiles || [];
  const item = p => `<button data-id="${p.id}" aria-pressed="${set.selectedId === p.id}">${flag(p.country)}<span class="ptype">${esc(PROTO_LABEL[p.type] || p.type)}</span><span class="nm">${esc(p.name)}</span><span class="lat ${latClass(p.latency)}">${p.latency ? fa(Math.round(p.latency)) : ''}</span></button>`;
  Modal.open(`<div class="dialog sm" role="dialog" aria-modal="true"><header><h3>انتخاب سرور</h3></header><div class="body">
    <button class="auto-card ${set.selectedId === 'auto' ? 'on' : ''}" data-id="auto">${icon('spark')}<span><b>انتخاب هوشمند</b><small>رهگذر از روی تجربه‌اش روی این شبکه، بهترین سرور را تست و انتخاب می‌کند و اگر قطع شد خودش عوض می‌کند.</small></span></button>
    <div class="search" style="margin:10px 0">${icon('search')}<input class="input" id="pq" placeholder="جستجو"></div>
    <div class="list-pick" id="pl">${ps.map(item).join('') || '<div class="empty">هنوز سروری اضافه نشده</div>'}</div></div>
    <footer><button class="btn ghost" data-nav="servers" onclick="Modal.close()">مدیریت سرورها</button></footer></div>`, m => {
    m.addEventListener('click', async e => {
      const b = e.target.closest('[data-id]');
      if (!b) return;
      await API.call('Select', b.dataset.id);
      S.st.settings.selectedId = b.dataset.id;
      Modal.close();
      render();
    });
    m.querySelector('#pq').oninput = e => {
      const q = e.target.value.trim().toLowerCase();
      m.querySelector('#pl').innerHTML = ps.filter(p => !q || (p.name + ' ' + p.server + ' ' + p.type).toLowerCase().includes(q)).map(item).join('');
    };
  });
}

// =====================================================================
// SERVERS
// =====================================================================
function filteredProfiles() {
  const q = S.query.trim().toLowerCase();
  return (S.st.profiles || []).filter(p =>
    (S.group === 'all' || p.group === S.group || (S.group === 'manual' && (!p.group || p.group === 'manual'))) &&
    (!q || (p.name + ' ' + p.server + ' ' + p.type + ' ' + (p.family || '')).toLowerCase().includes(q)));
}

function rowHTML(p) {
  const set = S.st.settings, st = S.st.status;
  const active = st.state === 'connected' && st.profileId === p.id;
  const chosen = set.selectedId === p.id;
  const t = S.tests[p.id];
  let lat;
  if (t && t.running) lat = '<span class="lat na">…</span>';
  else if (t && t.na) lat = `<span class="lat na" title="${esc(t.error)}">—</span>`;
  else if (t && !t.ok) lat = `<span class="lat bad" title="${esc(t.error)}">قطع</span>`;
  else if (t && t.ok) lat = `<span class="lat ${latClass(t.ms)}">${fa(t.ms)}</span>`;
  else lat = p.latency ? `<span class="lat ${latClass(p.latency)} " title="میانگین یادگرفته‌شده" style="opacity:.6">${fa(Math.round(p.latency))}</span>` : '<span class="lat na">—</span>';
  const pct = Math.round((p.success || 0) * 100);
  const mcls = pct >= 70 ? '' : pct >= 35 ? 'warn' : 'err';
  return `<div class="row ${active ? 'active' : ''} ${chosen ? 'chosen' : ''}" data-row="${p.id}">
    <input type="checkbox" class="check" data-sel="${p.id}" ${S.selected.has(p.id) ? 'checked' : ''} aria-label="انتخاب">
    <div class="ptype">${esc(PROTO_LABEL[p.type] || p.type)}<em>${esc(protoSub(p))}</em></div>
    <div class="name"><div class="name-line">${flag(p.country)}<b>${esc(p.name)}</b></div><small>${esc(p.server)}${p.port ? ':' + p.port : ''}${p.engineId !== 'sing-box' ? '  ·  ' + esc(p.engineId) : ''}</small></div>
    <div class="learn-cell" title="نرخ موفقیت یادگرفته‌شده روی این شبکه">${p.samples ? `<div class="meter ${mcls}"><i style="width:${pct}%"></i></div><span>${fa(pct)}٪ موفق · ${fa(p.samples)} نمونه</span>` : '<span>بدون سابقه</span>'}</div>
    <div data-lat="${p.id}">${lat}</div>
    <div class="acts">
      <button class="btn sm ${chosen ? 'primary' : ''}" data-use="${p.id}">${active ? 'فعال' : chosen ? 'انتخاب‌شده' : 'استفاده'}</button>
      <button class="btn ghost icon" data-test="${p.id}" title="تست تأخیر">${icon('bolt')}</button>
      <button class="btn ghost icon" data-edit="${p.id}" title="ویرایش">${icon('edit')}</button>
      <button class="btn ghost icon" data-copy="${p.id}" title="کپی لینک">${icon('copy')}</button>
      <button class="btn ghost icon" data-dup="${p.id}" title="کپی از سرور">${icon('dup')}</button>
      <button class="btn ghost icon danger" data-del="${p.id}" title="حذف">${icon('trash')}</button>
    </div></div>`;
}

Pages.servers = function () {
  const ps = S.st.profiles || [];
  const subs = S.st.subs || [];
  const count = g => ps.filter(p => g === 'all' || p.group === g || (g === 'manual' && (!p.group || p.group === 'manual'))).length;
  const tabs = [['all', 'همه'], ['manual', 'دستی'], ...subs.map(s => [s.id, s.name])];
  const list = filteredProfiles();
  const sub = subs.find(s => s.id === S.group);
  const set = S.st.settings;
  return `
  <div class="page-head"><div><h1>سرورها</h1><p>لینک، اشتراک، فایل OpenVPN یا WireGuard را وارد کنید. ستون موفقیت، چیزی است که رهگذر از اتصال‌های قبلی روی همین شبکه یاد گرفته.</p></div>
    <div class="actions">
      <button class="btn primary" id="impClip">${icon('clip')}از کلیپ‌بورد</button>
      <button class="btn" id="addSub">${icon('link')}اشتراک</button>
      <button class="btn" id="impFile">${icon('file')}فایل</button>
      <button class="btn" id="addMan">${icon('plus')}دستی</button>
    </div></div>
  <button class="auto-card ${set.selectedId === 'auto' ? 'on' : ''}" data-use="auto">${icon('spark')}<span><b>انتخاب هوشمند ${set.selectedId === 'auto' ? '(فعال)' : ''}</b><small>به‌جای یک سرور ثابت، هر بار بهترین سرور را بر اساس تست زنده و تجربه قبلی انتخاب کن.</small></span></button>
  <div class="tabs" role="tablist">${tabs.map(([id, n]) => `<button role="tab" data-group="${id}" aria-selected="${S.group === id}">${esc(n)} <span class="count">${fa(count(id))}</span></button>`).join('')}</div>
  ${sub ? subStrip(sub) : ''}
  <div class="toolbar">
    <div class="search grow">${icon('search')}<input class="input" id="q" placeholder="جستجو در نام، آدرس یا پروتکل" value="${esc(S.query)}"></div>
    <button class="btn" id="testAll" ${S.testing ? 'disabled' : ''}>${icon('bolt')}${S.testing ? 'در حال تست…' : 'تست ' + (S.selected.size ? 'انتخاب‌شده‌ها' : 'همه')}</button>
    <button class="btn" id="sortBest">مرتب‌سازی بر اساس کیفیت</button>
    ${S.selected.size ? `<button class="btn" id="mkChain">${icon('chain')}زنجیره</button><button class="btn danger" id="delSel">${icon('trash')}حذف ${fa(S.selected.size)}</button>` : ''}
  </div>
  <div class="rows" id="rows">${list.length ? list.map(rowHTML).join('') : emptyServers()}</div>`;
};

function subStrip(s) {
  const used = (s.upload || 0) + (s.download || 0);
  const quota = s.total ? `${bytesStr(used)} از ${bytesStr(s.total)}` : '';
  const exp = s.expire ? (s.expire > Date.now() / 1000 ? fa(Math.ceil((s.expire - Date.now() / 1000) / 86400)) + ' روز مانده' : 'منقضی شده') : '';
  return `<div class="sub-strip"><b>${esc(s.name)}</b><span class="mute">${[quota, exp, 'به‌روزرسانی ' + ago(s.lastUpdate)].filter(Boolean).join('  ·  ')}</span>
    ${s.lastError ? `<span class="badge err" title="${esc(s.lastError)}">خطا در دریافت</span>` : ''}<span class="grow"></span>
    <button class="btn sm" data-subup="${s.id}">${icon('refresh')}به‌روزرسانی</button><button class="btn sm ghost" data-subedit="${s.id}">${icon('edit')}</button><button class="btn sm ghost danger" data-subdel="${s.id}">${icon('trash')}</button></div>`;
}

function emptyServers() {
  return `<div class="empty"><b>هنوز سروری اینجا نیست</b>لینک‌های vless، vmess، trojan، ss، hy2 و … را کپی کنید و «از کلیپ‌بورد» را بزنید، یا یک اشتراک اضافه کنید.<br><button class="btn primary" id="impClip2">${icon('clip')}وارد کردن از کلیپ‌بورد</button></div>`;
}

Pages.serverRowUpdate = function (id) {
  const p = profileById(id);
  const row = document.querySelector(`[data-row="${id}"]`);
  if (!p || !row) return;
  const tmp = document.createElement('div');
  tmp.innerHTML = rowHTML(p);
  row.replaceWith(tmp.firstElementChild);
};

function showImport(r) {
  if (!r) return;
  if (r.added) toast(fa(r.added) + ' سرور اضافه شد', 'ok');
  else if (!r.errors || !r.errors.length) toast('سرور جدیدی پیدا نشد', 'warn');
  if (r.errors && r.errors.length) toast(r.errors.slice(0, 3).join('\n'), 'error');
}

Pages.serversMount = function (v) {
  const reload = async () => { S.st = await API.call('GetState'); render(); };
  const imp = async () => { showImport(await API.call('ImportClipboard')); reload(); };
  v.querySelector('#impClip').onclick = imp;
  const i2 = v.querySelector('#impClip2'); if (i2) i2.onclick = imp;
  v.querySelector('#impFile').onclick = async () => { showImport(await API.call('ImportFiles')); reload(); };
  v.querySelector('#addSub').onclick = () => openSubEditor();
  v.querySelector('#addMan').onclick = () => openEditor({ type: 'vless', network: 'tcp', security: 'reality', port: 443 });
  const q = v.querySelector('#q');
  q.oninput = () => { S.query = q.value; v.querySelector('#rows').innerHTML = filteredProfiles().map(rowHTML).join('') || emptyServers(); };
  v.querySelector('#testAll').onclick = () => {
    const ids = S.selected.size ? [...S.selected] : filteredProfiles().map(p => p.id);
    if (ids.length) API.call('TestProfiles', ids);
  };
  v.querySelector('#sortBest').onclick = async () => {
    const ps = [...S.st.profiles];
    const score = p => { const t = S.tests[p.id]; const live = t && t.ok ? 1 / (1 + t.ms / 300) : t && !t.ok && !t.na ? -1 : 0; return live * 2 + (p.success || 0) - (p.latency || 900) / 3000; };
    ps.sort((a, b) => score(b) - score(a));
    await API.call('ReorderProfiles', ps.map(p => p.id));
    reload();
  };
  const ch = v.querySelector('#mkChain');
  if (ch) ch.onclick = () => openEditor({ type: 'chain', chain: [...S.selected], name: 'زنجیره' });
  const ds = v.querySelector('#delSel');
  if (ds) ds.onclick = async () => {
    if (!await confirmBox('حذف سرورها', fa(S.selected.size) + ' سرور حذف شود؟', 'حذف', true)) return;
    await API.call('DeleteProfiles', [...S.selected]); S.selected.clear(); reload();
  };
  v.onclick = async e => {
    const t = e.target.closest('button, input');
    if (!t) return;
    const d = t.dataset;
    try {
      if (d.group) { S.group = d.group; S.selected.clear(); render(); }
      else if (d.sel) { t.checked ? S.selected.add(d.sel) : S.selected.delete(d.sel); render(); }
      else if (d.use) { await API.call('Select', d.use); S.st.settings.selectedId = d.use; render(); if (S.st.status.state !== 'connected') toast('انتخاب شد؛ از صفحه خانه وصل شوید', 'info'); }
      else if (d.test) API.call('TestProfiles', [d.test]);
      else if (d.edit) openEditor(Object.assign({}, profileById(d.edit)));
      else if (d.copy) { await API.call('CopyLink', d.copy); toast('لینک کپی شد', 'ok'); }
      else if (d.dup) { await API.call('DuplicateProfile', d.dup); reload(); }
      else if (d.del) {
        const p = profileById(d.del);
        if (await confirmBox('حذف سرور', `«${p.name}» حذف شود؟`, 'حذف', true)) { await API.call('DeleteProfiles', [d.del]); reload(); }
      }
      else if (d.subup) { toast('در حال دریافت اشتراک…', 'info'); const n = await API.call('UpdateSubscription', d.subup); toast(fa(n) + ' سرور دریافت شد', 'ok'); reload(); }
      else if (d.subedit) openSubEditor((S.st.subs || []).find(s => s.id === d.subedit));
      else if (d.subdel) {
        if (await confirmBox('حذف اشتراک', 'اشتراک و همه سرورهایش حذف شود؟', 'حذف', true)) { await API.call('DeleteSubscription', d.subdel); S.group = 'all'; reload(); }
      }
    } catch (err) { toast(errText(err), 'error'); }
  };
};

function openSubEditor(sub) {
  const s = sub || { name: '', url: '', autoUpdate: true, intervalH: 12, viaProxy: true, userAgent: '' };
  Modal.open(`<div class="dialog sm" role="dialog" aria-modal="true"><header><h3>${sub ? 'ویرایش اشتراک' : 'افزودن اشتراک'}</h3></header><div class="body">
    <div class="grid2">
      <label class="field span2"><span>آدرس اشتراک</span><input class="input ltr" name="url" value="${esc(s.url)}" placeholder="https://…" autofocus></label>
      <label class="field span2"><span>نام</span><input class="input" name="name" value="${esc(s.name)}" placeholder="مثلاً اشتراک اصلی"></label>
      <label class="field"><span>به‌روزرسانی هر (ساعت)</span><input class="input ltr" type="number" min="1" name="intervalH" value="${s.intervalH || 12}"></label>
      <label class="field"><span>User-Agent</span><input class="input ltr" name="userAgent" value="${esc(s.userAgent || '')}" placeholder="v2rayN/7.4"></label>
    </div>
    ${switchRow('autoUpdate', s.autoUpdate, 'به‌روزرسانی خودکار', '')}
    ${switchRow('viaProxy', s.viaProxy, 'دریافت از داخل تونل', 'اگر آدرس اشتراک فیلتر است روشن بماند')}
  </div><footer><button class="btn primary" data-ok>${sub ? 'ذخیره' : 'افزودن و دریافت'}</button><button class="btn ghost" data-no>انصراف</button></footer></div>`, m => {
    m.querySelector('[data-no]').onclick = Modal.close;
    m.querySelector('[data-ok]').onclick = async () => {
      const f = n => m.querySelector(`[name=${n}]`);
      try {
        if (sub) {
          await API.call('SaveSubscription', Object.assign({}, sub, { name: f('name').value, url: f('url').value.trim(), intervalH: +f('intervalH').value, userAgent: f('userAgent').value, autoUpdate: f('autoUpdate').checked, viaProxy: f('viaProxy').checked }));
        } else {
          toast('در حال دریافت اشتراک…', 'info');
          const r = await API.call('AddSubscription', f('name').value, f('url').value.trim());
          toast(fa(r.count || 0) + ' سرور از اشتراک دریافت شد', 'ok');
        }
        Modal.close();
        S.st = await API.call('GetState'); render();
      } catch (e) { toast(errText(e), 'error'); S.st = await API.call('GetState'); render(); }
    };
  });
}

// ---------- profile editor ----------
const TYPES = [
  ['vless', 'VLESS'], ['vmess', 'VMess'], ['trojan', 'Trojan'], ['shadowsocks', 'Shadowsocks'], ['ssr', 'ShadowsocksR'],
  ['hysteria2', 'Hysteria2'], ['hysteria', 'Hysteria'], ['tuic', 'TUIC'], ['wireguard', 'WireGuard'], ['anytls', 'AnyTLS'],
  ['openvpn', 'OpenVPN'], ['socks', 'SOCKS5'], ['http', 'HTTP / HTTPS'], ['ssh', 'SSH'],
  ['pptp', 'PPTP (ویندوز)'], ['l2tp', 'L2TP/IPsec (ویندوز)'], ['sstp', 'SSTP (ویندوز)'], ['ikev2', 'IKEv2 (ویندوز)'], ['chain', 'زنجیره پروکسی']
];
const V2 = ['vless', 'vmess', 'trojan'];
const FIELDS = [
  { k: 'uuid', l: 'UUID', t: ['vless', 'vmess', 'tuic'], ltr: 1 },
  { k: 'password', l: 'رمز عبور', t: ['trojan', 'shadowsocks', 'ssr', 'hysteria2', 'tuic', 'anytls', 'socks', 'http', 'ssh', 'openvpn', 'pptp', 'l2tp', 'sstp', 'ikev2'], ltr: 1 },
  { k: 'username', l: 'نام کاربری', t: ['socks', 'http', 'ssh', 'openvpn', 'pptp', 'l2tp', 'sstp', 'ikev2'], ltr: 1 },
  { k: 'method', l: 'رمزنگاری', t: ['shadowsocks', 'ssr'], s: ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305', 'aes-128-gcm', 'aes-256-gcm', 'chacha20-ietf-poly1305', 'xchacha20-ietf-poly1305', 'aes-256-cfb', 'aes-128-cfb', 'chacha20-ietf', 'rc4-md5', 'none'] },
  { k: 'method', l: 'امنیت VMess', t: ['vmess'], s: ['auto', 'aes-128-gcm', 'chacha20-poly1305', 'none', 'zero'] },
  { k: 'alterId', l: 'Alter ID', t: ['vmess'], n: 1 },
  { k: 'flow', l: 'Flow', t: ['vless'], s: ['', 'xtls-rprx-vision'] },
  { k: 'network', l: 'انتقال (Transport)', t: V2, s: ['tcp', 'ws', 'grpc', 'httpupgrade', 'xhttp', 'http', 'kcp', 'quic'] },
  { k: 'path', l: 'Path', t: V2, ltr: 1, net: ['ws', 'httpupgrade', 'xhttp', 'http', 'tcp'] },
  { k: 'host', l: 'Host', t: V2, ltr: 1, net: ['ws', 'httpupgrade', 'xhttp', 'http', 'tcp'] },
  { k: 'serviceName', l: 'Service name', t: V2, ltr: 1, net: ['grpc'] },
  { k: 'xmode', l: 'حالت XHTTP', t: V2, s: ['auto', 'packet-up', 'stream-up', 'stream-one'], net: ['xhttp'] },
  { k: 'headerType', l: 'Header', t: V2, s: ['', 'http', 'none', 'srtp', 'utp', 'wechat-video', 'dtls', 'wireguard'], net: ['tcp', 'kcp'] },
  { k: 'security', l: 'امنیت لایه انتقال', t: V2, s: ['', 'tls', 'reality'] },
  { k: 'security', l: 'TLS', t: ['http'], s: ['', 'tls'] },
  { k: 'sni', l: 'SNI', t: [...V2, 'hysteria2', 'hysteria', 'tuic', 'anytls', 'http'], ltr: 1 },
  { k: 'fp', l: 'Fingerprint (uTLS)', t: V2, s: ['', 'chrome', 'firefox', 'safari', 'edge', 'ios', 'android', 'random', 'randomized'] },
  { k: 'alpn', l: 'ALPN', t: [...V2, 'hysteria', 'tuic'], ltr: 1 },
  { k: 'pbk', l: 'Reality public key', t: V2, ltr: 1, sec: 'reality' },
  { k: 'sid', l: 'Reality short ID', t: V2, ltr: 1, sec: 'reality' },
  { k: 'spx', l: 'SpiderX', t: V2, ltr: 1, sec: 'reality' },
  { k: 'xextra', l: 'XHTTP extra (JSON)', t: V2, ltr: 1, net: ['xhttp'], area: 1 },
  { k: 'insecure', l: 'نادیده گرفتن خطای گواهی', t: [...V2, 'hysteria2', 'hysteria', 'tuic', 'anytls'], b: 1 },
  { k: 'plugin', l: 'Plugin', t: ['shadowsocks'], s: ['', 'obfs-local', 'v2ray-plugin'] },
  { k: 'pluginOpts', l: 'Plugin options', t: ['shadowsocks'], ltr: 1 },
  { k: 'protocol', l: 'Protocol', t: ['ssr'], s: ['origin', 'auth_sha1_v4', 'auth_aes128_md5', 'auth_aes128_sha1', 'auth_chain_a', 'auth_chain_b'] },
  { k: 'protocolParam', l: 'Protocol param', t: ['ssr'], ltr: 1 },
  { k: 'obfs', l: 'Obfs', t: ['ssr'], s: ['plain', 'http_simple', 'http_post', 'tls1.2_ticket_auth', 'random_head'] },
  { k: 'obfsParam', l: 'Obfs param', t: ['ssr'], ltr: 1 },
  { k: 'obfs', l: 'Obfs', t: ['hysteria2'], s: ['', 'salamander'] },
  { k: 'obfsPassword', l: 'رمز Obfs', t: ['hysteria2', 'hysteria'], ltr: 1 },
  { k: 'hyAuth', l: 'Auth', t: ['hysteria'], ltr: 1 },
  { k: 'ports', l: 'پرش پورت (مثلاً 20000-30000)', t: ['hysteria2', 'hysteria'], ltr: 1 },
  { k: 'upMbps', l: 'آپلود (Mbps)', t: ['hysteria2', 'hysteria'], n: 1 },
  { k: 'downMbps', l: 'دانلود (Mbps)', t: ['hysteria2', 'hysteria'], n: 1 },
  { k: 'congestion', l: 'Congestion', t: ['tuic'], s: ['bbr', 'cubic', 'new_reno'] },
  { k: 'udpRelayMode', l: 'UDP relay', t: ['tuic'], s: ['native', 'quic'] },
  { k: 'privateKey', l: 'Private key', t: ['wireguard', 'ssh'], ltr: 1 },
  { k: 'peerPublicKey', l: 'Peer public key', t: ['wireguard'], ltr: 1 },
  { k: 'preSharedKey', l: 'Pre-shared key', t: ['wireguard'], ltr: 1 },
  { k: 'localAddress', l: 'آدرس داخلی (با کاما)', t: ['wireguard'], ltr: 1, list: 1 },
  { k: 'reserved', l: 'Reserved (مثلاً 1,2,3)', t: ['wireguard'], ltr: 1, ints: 1 },
  { k: 'mtu', l: 'MTU', t: ['wireguard'], n: 1 },
  { k: 'psk', l: 'کلید اشتراکی (PSK)', t: ['l2tp'], ltr: 1 },
  { k: 'ovpnNet', l: 'پروتکل انتقال', t: ['openvpn'], s: ['', 'udp', 'tcp'] },
  { k: 'ovpn', l: 'محتوای فایل .ovpn', t: ['openvpn'], ltr: 1, area: 1 },
  { k: 'engine', l: 'موتور', t: [...V2, 'shadowsocks'], s: ['auto', 'sing-box', 'xray', 'mihomo'] },
];

function fieldHTML(f, p) {
  let v = p[f.k];
  if (f.list && Array.isArray(v)) v = v.join(', ');
  if (f.ints && Array.isArray(v)) v = v.join(',');
  if (v == null) v = '';
  const span = f.area ? 'span2' : '';
  if (f.b) return `<div class="span2">${switchRow(f.k, !!v, f.l, '')}</div>`;
  if (f.s) return `<label class="field ${span}"><span>${f.l}</span><select class="input ltr" name="${f.k}">${f.s.map(o => `<option value="${o}" ${String(v) === o ? 'selected' : ''}>${o || '—'}</option>`).join('')}</select></label>`;
  if (f.area) return `<label class="field span2"><span>${f.l}</span><textarea class="input ltr" name="${f.k}" rows="${f.k === 'ovpn' ? 10 : 3}">${esc(v)}</textarea></label>`;
  return `<label class="field ${span}"><span>${f.l}</span><input class="input ${f.ltr || f.n ? 'ltr' : ''}" ${f.n ? 'type="number"' : ''} name="${f.k}" value="${esc(v)}"></label>`;
}

function editorBody(p) {
  const ras = ['pptp', 'l2tp', 'sstp', 'ikev2'].includes(p.type);
  let h = `<div class="grid2">
    <label class="field"><span>نوع</span><select class="input" name="type">${TYPES.filter(([v]) => !isAndroid() || !ANDROID_UNSUPPORTED.includes(v)).map(([v, l]) => `<option value="${v}" ${p.type === v ? 'selected' : ''}>${l}</option>`).join('')}</select></label>
    <label class="field"><span>نام</span><input class="input" name="name" value="${esc(p.name || '')}"></label>`;
  if (p.type === 'chain') {
    const ps = (S.st.profiles || []).filter(x => x.type !== 'chain' && x.engineId === 'sing-box' && x.id !== p.id);
    const chain = p.chain || [];
    h += `</div><p class="lead" style="margin-top:14px">ترتیب عبور ترافیک: از اولین گره به آخرین. فقط پروتکل‌هایی که sing-box اجرا می‌کند در زنجیره مجازند.</p>
      <div class="list-pick" id="chainList">${chain.map((id, i) => { const x = profileById(id); return x ? `<button type="button" data-hop="${i}" aria-pressed="true"><span class="ptype">${fa(i + 1)}</span><span class="nm">${esc(x.name)}</span><span class="faint">حذف</span></button>` : ''; }).join('')}</div>
      <label class="field" style="margin-top:10px"><span>افزودن گره</span><select class="input" id="addHop"><option value="">—</option>${ps.map(x => `<option value="${x.id}">${esc(x.name)} (${esc(PROTO_LABEL[x.type])})</option>`).join('')}</select></label>`;
    return h;
  }
  if (p.type !== 'openvpn') {
    h += `<label class="field"><span>آدرس سرور</span><input class="input ltr" name="server" value="${esc(p.server || '')}"></label>
      <label class="field"><span>پورت${ras ? ' (اختیاری)' : ''}</span><input class="input ltr" type="number" name="port" value="${p.port || ''}"></label>`;
  }
  for (const f of FIELDS) {
    if (!f.t.includes(p.type)) continue;
    if (f.net && !f.net.includes(p.network || 'tcp')) continue;
    if (f.sec && p.security !== f.sec) continue;
    h += fieldHTML(f, p);
  }
  h += '</div>';
  if (ras) h += `<p class="lead" style="margin-top:14px">این اتصال با VPN داخلی ویندوز برقرار می‌شود و کل سیستم را پوشش می‌دهد؛ حالت پروکسی و مسیریابی هوشمند روی آن اعمال نمی‌شود.${p.type === 'pptp' ? ' PPTP امنیت پایینی دارد.' : ''}</p>`;
  return h;
}

function readEditor(m, p) {
  const out = Object.assign({}, p);
  m.querySelectorAll('[name]').forEach(el => {
    const k = el.name;
    const f = FIELDS.find(x => x.k === k && x.t.includes(out.type));
    if (el.type === 'checkbox') out[k] = el.checked;
    else if (el.type === 'number' || (f && f.n)) out[k] = el.value === '' ? 0 : Number(el.value);
    else if (f && f.list) out[k] = el.value.split(/[,\s]+/).filter(Boolean);
    else if (f && f.ints) out[k] = el.value.split(/[,\s]+/).filter(Boolean).map(Number);
    else out[k] = el.value;
  });
  if (out.engine === 'auto') out.engine = '';
  return out;
}

function openEditor(p) {
  const isNew = !p.id;
  Modal.open(`<div class="dialog" role="dialog" aria-modal="true"><header><h3>${isNew ? 'سرور جدید' : 'ویرایش سرور'}</h3></header>
    <div class="body" id="edBody">${editorBody(p)}</div>
    <footer><button class="btn primary" data-ok>ذخیره</button><button class="btn ghost" data-no>انصراف</button></footer></div>`, m => {
    const body = m.querySelector('#edBody');
    const rerender = () => { p = readEditor(m, p); body.innerHTML = editorBody(p); wire(); };
    const wire = () => {
      body.querySelectorAll('[name=type], [name=network], [name=security]').forEach(el => el.onchange = rerender);
      const add = body.querySelector('#addHop');
      if (add) add.onchange = () => { if (add.value) { p.chain = [...(p.chain || []), add.value]; body.innerHTML = editorBody(p); wire(); } };
      body.querySelectorAll('[data-hop]').forEach(b => b.onclick = () => { p.chain.splice(+b.dataset.hop, 1); body.innerHTML = editorBody(p); wire(); });
    };
    wire();
    m.querySelector('[data-no]').onclick = Modal.close;
    m.querySelector('[data-ok]').onclick = async () => {
      try {
        const out = readEditor(m, p);
        out.chain = p.chain;
        await API.call('SaveProfile', out);
        Modal.close();
        toast('ذخیره شد', 'ok');
        S.st = await API.call('GetState'); render();
      } catch (e) { toast(errText(e), 'error'); }
    };
  });
}

// =====================================================================
// ROUTING
// =====================================================================
Pages.routing = function () {
  const s = S.st.settings;
  const modes = [
    ['smart', 'هوشمند', 'سایت‌های ایرانی مستقیم؛ هر سایت جدید تست می‌شود و فقط اگر فیلتر یا تحریم بود از تونل می‌رود.'],
    ['rules', 'فقط قوانین', 'سایت‌ها و IPهای ایرانی مستقیم، بقیه از تونل. بدون یادگیری.'],
    ['global', 'همه از تونل', 'همه ترافیک (جز شبکه محلی) از سرور عبور می‌کند.'],
    ['direct', 'مستقیم', 'همه مستقیم، جز دامنه‌هایی که در لیست «همیشه از تونل» نوشته‌اید.'],
  ];
  const ta = (name, label, hint, list) => `<label class="field"><span>${label}</span><textarea class="input ltr" name="${name}" placeholder="${hint}">${esc((list || []).join('\n'))}</textarea><small>هر خط یک دامنه؛ زیردامنه‌ها هم شامل می‌شوند.</small></label>`;
  return `
  <div class="page-head"><div><h1>مسیریابی</h1><p>تعیین کنید چه ترافیکی از تونل برود و چه ترافیکی مستقیم. تغییرات بلافاصله روی اتصال فعال اعمال می‌شود.</p></div></div>
  <div class="route-modes">${modes.map(([id, t, d]) => `<button class="route-mode" data-rm="${id}" aria-pressed="${s.routingMode === id}"><b>${t}</b><small>${d}</small></button>`).join('')}</div>
  <div class="section"><h2>قوانین عمومی</h2><div class="panel pad" id="rgen">
    ${switchRow('bypassIran', s.bypassIran, 'سایت‌های ایرانی مستقیم', 'دامنه‌های ‎.ir، لیست geosite-ir و IPهای ایران بدون تونل باز می‌شوند')}
    ${switchRow('learnDomains', s.learnDomains, 'یادگیری خودکار سایت‌ها', 'در حالت هوشمند، سایت‌هایی که می‌بینید در پس‌زمینه تست و دسته‌بندی می‌شوند')}
    ${switchRow('blockAds', s.blockAds, 'مسدود کردن تبلیغات و بدافزار', 'با لیست‌های geosite-category-ads-all، malware و phishing')}
    ${switchRow('bypassLan', s.bypassLan, 'شبکه محلی مستقیم', 'آدرس‌های 192.168.x.x، 10.x.x.x و … از تونل نمی‌روند')}
  </div></div>
  ${isAndroid() ? perAppSection(s) : ''}
  <div class="section"><h2>دامنه‌های شما</h2><div class="grid3" id="rlists">
    ${ta('proxyDomains', 'همیشه از تونل', 'youtube.com', s.proxyDomains)}
    ${ta('directDomains', 'همیشه مستقیم', 'aparat.com', s.directDomains)}
    ${ta('blockDomains', 'مسدود', 'ads.example.com', s.blockDomains)}
  </div>
  <label class="field win-only" style="margin-top:14px"><span>برنامه‌هایی که در حالت TUN مستقیم بروند</span><textarea class="input ltr" name="directProcesses" placeholder="AnyDesk.exe" style="min-height:70px">${esc((s.directProcesses || []).join('\n'))}</textarea></label>
  <div style="margin-top:12px"><button class="btn primary" id="saveLists">ذخیره لیست‌ها</button></div></div>`;
};

Pages.routingMount = function (v) {
  v.querySelectorAll('[data-pam]').forEach(b => b.onclick = async () => { await saveSettings({ perAppMode: b.dataset.pam }); render(); });
  const pa = v.querySelector('#pickApps');
  if (pa) pa.onclick = openAppPicker;
  v.querySelectorAll('[data-rm]').forEach(b => b.onclick = async () => { await saveSettings({ routingMode: b.dataset.rm }); render(); });
  v.querySelectorAll('#rgen input[type=checkbox]').forEach(el => el.onchange = () => saveSettings({ [el.name]: el.checked }));
  v.querySelector('#saveLists').onclick = async () => {
    const lines = n => v.querySelector(`[name=${n}]`).value.split(/\n+/).map(x => x.trim()).filter(Boolean);
    const dp = v.querySelector('[name=directProcesses]');
    await saveSettings({ proxyDomains: lines('proxyDomains'), directDomains: lines('directDomains'), blockDomains: lines('blockDomains'), directProcesses: dp ? lines('directProcesses') : S.st.settings.directProcesses });
    toast('لیست‌ها ذخیره شد', 'ok');
  };
};

// =====================================================================
// LEARNING
// =====================================================================
Pages.learn = function () {
  const L = S.learning;
  if (!L) return '<div class="empty">در حال بارگذاری…</div>';
  const stateBadge = { good: '<span class="badge ok">سالم</span>', weak: '<span class="badge warn">ضعیف</span>', blocked: '<span class="badge err">مسدود</span>', unknown: '<span class="badge">نامعلوم</span>' };
  const fams = (L.families || []).map(f => {
    const pct = Math.round(f.success * 100);
    return `<div class="hrow"><span class="fam">${esc(f.family)}</span><div class="meter ${f.state === 'good' ? '' : f.state === 'weak' ? 'warn' : 'err'}"><i style="width:${pct}%"></i></div><span class="num">${fa(pct)}٪</span>${stateBadge[f.state] || ''}</div>`;
  }).join('');
  const rank = (L.ranking || []).slice(0, 10).map((r, i) => `<div class="fact"><span>${fa(i + 1)}. ${flag(r.country)} ${esc(r.name)} <span class="faint ltr">${esc(r.family)}</span></span><b class="num">${r.samples ? fa(Math.round(r.success * 100)) + '٪ · ' + fa(Math.round(r.latency)) + 'ms' : 'بدون سابقه'}</b></div>`).join('');
  const vb = { direct: '<span class="badge ok">مستقیم</span>', proxy: '<span class="badge info">تونل</span>', unknown: '<span class="badge">نامعلوم</span>' };
  const dq = (S.dq || '').toLowerCase();
  const doms = (L.domains || []).filter(d => !dq || d.domain.includes(dq)).slice(0, 200).map(d => `<tr>
    <td class="d">${esc(d.domain)}</td><td>${vb[d.verdict] || ''}${d.source === 'user' ? ' <span class="badge">دستی</span>' : ''}</td>
    <td class="r">${esc(d.reason || '')}</td><td class="num mute">${fa(d.hits || 0)}</td>
    <td><button class="btn sm ghost" data-dv="${esc(d.domain)}" data-to="proxy">تونل</button><button class="btn sm ghost" data-dv="${esc(d.domain)}" data-to="direct">مستقیم</button><button class="btn sm ghost" data-dv="${esc(d.domain)}" data-to="auto">بازیادگیری</button></td></tr>`).join('');
  return `
  <div class="page-head"><div><h1>یادگیری</h1><p>رهگذر از هر تست و هر اتصال یاد می‌گیرد: کدام پروتکل روی این شبکه کار می‌کند، کدام سرور در این ساعت بهتر است و کدام سایت واقعاً فیلتر است.</p></div>
    <div class="actions"><button class="btn" id="relearn">${icon('refresh')}تازه‌سازی</button></div></div>
  <div class="minis">
    <div class="mini"><span>شبکه فعلی</span><b style="font-size:16px">${esc(netName(L.netLabel))}</b></div>
    <div class="mini"><span>نمونه‌های ثبت‌شده</span><b>${fa(L.samples || 0)}</b></div>
    <div class="mini"><span>سایت‌های مستقیم</span><b>${fa(L.direct || 0)}</b></div>
    <div class="mini"><span>سایت‌های نیازمند تونل</span><b>${fa(L.proxy || 0)}</b></div>
  </div>
  <div class="section"><h2>سلامت پروتکل‌ها روی این شبکه</h2><div class="panel pad health">${fams || '<div class="mute">هنوز داده‌ای نیست. از صفحه سرورها «تست همه» را بزنید تا یادگیری شروع شود.</div>'}</div></div>
  <div class="section"><h2>رتبه‌بندی سرورها <small>بر اساس همین شبکه و همین ساعت از روز</small></h2><div class="panel pad">${rank || '<div class="mute">سروری نیست</div>'}</div></div>
  <div class="section"><h2>سایت‌های یادگرفته‌شده</h2>
    <div class="toolbar"><div class="search grow">${icon('search')}<input class="input ltr" id="dq" placeholder="search domain" value="${esc(S.dq || '')}"></div>
      <input class="input ltr" id="newDom" placeholder="example.com" style="width:200px"><button class="btn" id="addProxy">افزودن به تونل</button>
      <button class="btn danger" id="resetDom">فراموشی سایت‌ها</button><button class="btn danger" id="resetSrv">فراموشی سابقه سرورها</button></div>
    <div class="panel" style="overflow:auto"><table class="domain-table"><thead><tr><th>دامنه</th><th>مسیر</th><th>دلیل</th><th>بازدید</th><th></th></tr></thead><tbody>${doms || '<tr><td colspan="5" class="mute">پس از اتصال در حالت هوشمند، سایت‌هایی که باز می‌کنید اینجا می‌آیند.</td></tr>'}</tbody></table></div></div>`;
};

Pages.learnMount = function (v) {
  if (!S.learning) return;
  v.querySelector('#relearn').onclick = loadLearning;
  const dq = v.querySelector('#dq');
  dq.oninput = () => { S.dq = dq.value; const pos = dq.selectionStart; render(); const n = document.getElementById('dq'); n.focus(); n.setSelectionRange(pos, pos); };
  v.querySelector('#addProxy').onclick = async () => { const d = v.querySelector('#newDom').value.trim(); if (d) { await API.call('SetDomain', d, 'proxy'); loadLearning(); } };
  v.querySelector('#resetDom').onclick = async () => { if (await confirmBox('فراموشی سایت‌ها', 'همه سایت‌های یادگرفته‌شده (به‌جز انتخاب‌های دستی) پاک شوند؟', 'پاک کن', true)) { await API.call('ResetLearning', 'domains'); loadLearning(); } };
  v.querySelector('#resetSrv').onclick = async () => { if (await confirmBox('فراموشی سابقه سرورها', 'آمار کیفیت سرورها و پروتکل‌ها پاک شود؟', 'پاک کن', true)) { await API.call('ResetLearning', 'servers'); loadLearning(); } };
  v.querySelectorAll('[data-dv]').forEach(b => b.onclick = async () => { await API.call('SetDomain', b.dataset.dv, b.dataset.to); loadLearning(); });
};

// =====================================================================
// ENGINES
// =====================================================================
function engineCard(e) {
  const busy = e.busy;
  const ver = e.installed ? esc(e.version || '?') : 'نصب نشده';
  const latest = e.latest ? esc(e.latest) : '—';
  let action;
  if (busy) action = `<button class="btn" disabled>${esc(e.message || 'در حال کار…')}</button>`;
  else if (!e.installed) action = `<button class="btn primary" data-eup="${e.id}">${icon('download')}نصب</button>`;
  else if (e.hasUpdate) action = `<button class="btn primary" data-eup="${e.id}">${icon('download')}به‌روزرسانی به ${latest}</button>`;
  else action = `<button class="btn" data-eup="${e.id}">${icon('refresh')}نصب مجدد آخرین نسخه</button>`;
  if (e.builtin) {
    return `<div class="card" data-eng="${e.id}">
    <h3>${esc(e.name)} <span class="badge ok">داخل برنامه</span></h3>
    <p>${esc(e.desc)}</p>
    <div class="ver"><div><span>نسخه: </span><b>${esc(e.version || '?')}</b></div></div>
    <div class="chips">${(e.protocols || []).map(x => `<span class="badge">${esc(x)}</span>`).join('')}</div>
    <p class="faint" style="font-size:12px">روی اندروید موتورها داخل خود برنامه‌اند و با آپدیت برنامه به‌روز می‌شوند.</p></div>`;
  }
  return `<div class="card" data-eng="${e.id}">
    <h3>${esc(e.name)} ${e.required ? '<span class="badge info">ضروری</span>' : ''} ${e.hasUpdate ? '<span class="badge warn">نسخه جدید</span>' : ''}</h3>
    <p>${esc(e.desc)}</p>
    <div class="ver"><div><span>نصب‌شده: </span><b>${ver}</b></div><div><span>آخرین: </span><b>${latest}</b></div></div>
    <div class="chips">${(e.protocols || []).map(x => `<span class="badge">${esc(x)}</span>`).join('')}</div>
    ${busy ? `<div class="progress"><i style="width:${Math.round((e.progress || 0) * 100)}%"></i></div>` : ''}
    ${e.error ? `<p style="color:var(--anar);user-select:text">${esc(e.error)}</p>` : ''}
    <div class="foot">${action}
      <button class="btn ghost" data-echeck="${e.id}" ${busy ? 'disabled' : ''}>بررسی نسخه</button>
      <button class="btn ghost" data-efile="${e.id}" ${busy ? 'disabled' : ''}>${icon('file')}نصب از فایل</button>
      ${e.hasBackup ? `<button class="btn ghost" data-eback="${e.id}" ${busy ? 'disabled' : ''}>${icon('undo')}نسخه قبلی</button>` : ''}
    </div></div>`;
}

Pages.engines = function () {
  const es = S.engines;
  const u = S.appUpdate;
  return `
  <div class="page-head"><div><h1>موتورها و به‌روزرسانی</h1><p>${isAndroid() ? 'موتورها داخل برنامه‌اند؛ قوانین ایران جداگانه به‌روز می‌شوند. آپدیت برنامه فایل APK جدید را از GitHub دریافت و نصب می‌کند.' : 'هر موتور جداگانه از مخزن رسمی خودش در GitHub به‌روز می‌شود و نسخه قبلی برای بازگشت نگه داشته می‌شود. اگر GitHub کند است، در تنظیمات یک میرور وارد کنید یا فایل را دستی نصب کنید.'}</p></div>
    <div class="actions"><button class="btn" id="checkAll">${icon('refresh')}بررسی همه</button><button class="btn primary" id="updAll">${icon('download')}نصب/به‌روزرسانی همه</button></div></div>
  <div class="panel pad" style="display:flex;gap:14px;align-items:center;margin-bottom:16px;flex-wrap:wrap">
    <div style="flex:1"><b>رهگذر</b> <span class="mute">نسخه <span class="ltr">${esc(S.st.version)}</span>${u ? (u.available ? ' — نسخه ' + esc(u.latest) + ' آماده نصب است' : ' — آخرین نسخه را دارید') : ''}</span>
      <div class="progress" style="margin-top:8px;${u && u.available ? '' : 'display:none'}"><i id="appUpBar" style="width:0"></i></div></div>
    <button class="btn" id="appCheck">${icon('refresh')}بررسی آپدیت برنامه</button>
    ${u && u.available ? `<button class="btn primary" id="appApply">${icon('download')}نصب و اجرای مجدد</button>` : ''}
  </div>
  ${es ? `<div class="cards">${es.map(engineCard).join('')}</div>` : '<div class="empty">در حال بارگذاری…</div>'}
  <div class="section"><h2>کدام پروتکل با کدام موتور</h2><div class="panel pad">
    <div class="fact"><span>VLESS / VMess / Trojan / Reality / WS / gRPC / HTTPUpgrade</span><b>sing-box</b></div>
    <div class="fact"><span>XHTTP، mKCP، VLESS Encryption، TCP-HTTP header</span><b>Xray-core</b></div>
    <div class="fact"><span>Shadowsocks، SOCKS5، HTTP(S)، SSH، AnyTLS</span><b>sing-box</b></div>
    <div class="fact"><span>Hysteria، Hysteria2، TUIC (QUIC)</span><b>sing-box</b></div>
    <div class="fact"><span>WireGuard، OpenVPN (TCP/UDP)</span><b>sing-box (بدون نیاز به درایور)</b></div>
    <div class="fact"><span>ShadowsocksR</span><b>${isAndroid() ? 'روی اندروید پشتیبانی نمی‌شود' : 'Mihomo'}</b></div>
    <div class="fact"><span>PPTP، L2TP/IPsec، SSTP، IKEv2</span><b>${isAndroid() ? 'فقط از تنظیمات VPN خود اندروید' : 'VPN داخلی ویندوز'}</b></div>
    <div class="fact"><span>TUN، DNS، زنجیره پروکسی، مسیریابی</span><b>sing-box</b></div>
  </div></div>`;
};

Pages.engineCardUpdate = function (e) {
  const el = document.querySelector(`[data-eng="${e.id}"]`);
  if (!el) return;
  const t = document.createElement('div');
  t.innerHTML = engineCard(e);
  el.replaceWith(t.firstElementChild);
};

Pages.enginesMount = function (v) {
  v.querySelector('#checkAll').onclick = async () => {
    for (const e of S.engines || []) { try { await API.call('CheckEngine', e.id); } catch (err) { toast(e.name + ': ' + errText(err), 'error'); } }
    loadEngines();
  };
  v.querySelector('#updAll').onclick = () => API.call('UpdateAllEngines');
  v.querySelector('#appCheck').onclick = async () => {
    try { S.appUpdate = await API.call('CheckAppUpdate'); render(); if (!S.appUpdate.available) toast('آخرین نسخه نصب است', 'ok'); }
    catch (e) { toast(errText(e), 'error'); }
  };
  const ap = v.querySelector('#appApply');
  if (ap) ap.onclick = async () => { ap.disabled = true; try { await API.call('ApplyAppUpdate'); } catch (e) { toast(errText(e), 'error'); ap.disabled = false; } };
  v.onclick = async ev => {
    const b = ev.target.closest('button');
    if (!b) return;
    const d = b.dataset;
    try {
      if (d.eup) API.call('UpdateEngine', d.eup);
      else if (d.echeck) { await API.call('CheckEngine', d.echeck); loadEngines(); }
      else if (d.efile) { await API.call('InstallEngineFromFile', d.efile); loadEngines(); S.st = await API.call('GetState'); }
      else if (d.eback) { await API.call('RollbackEngine', d.eback); toast('نسخه قبلی برگردانده شد', 'ok'); loadEngines(); }
    } catch (e) { toast(errText(e), 'error'); }
  };
};

// =====================================================================
// SETTINGS
// =====================================================================
Pages.settings = function () {
  const s = S.st.settings;
  const inp = (k, l, ltr, type, hint) => `<label class="field"><span>${l}</span><input class="input ${ltr ? 'ltr' : ''}" ${type ? `type="${type}"` : ''} name="${k}" value="${esc(s[k] == null ? '' : s[k])}">${hint ? `<small>${hint}</small>` : ''}</label>`;
  const sel = (k, l, opts) => `<label class="field"><span>${l}</span><select class="input" name="${k}">${opts.map(([v, t]) => `<option value="${v}" ${String(s[k]) === v ? 'selected' : ''}>${t}</option>`).join('')}</select></label>`;
  return `
  <div class="page-head"><div><h1>تنظیمات</h1><p>تغییرات خودکار ذخیره می‌شوند و اگر وصل باشید، اتصال با تنظیمات جدید از نو برقرار می‌شود.</p></div>
    <div class="actions win-only"><button class="btn" id="openData">${icon('folder')}پوشه داده‌ها</button></div></div>
  ${!S.st.admin && !isAndroid() ? `<div class="banner info"><span class="grow">برنامه بدون دسترسی مدیر اجرا شده؛ حالت TUN و اتصال‌های L2TP/IKEv2 ممکن است کار نکنند.</span><button class="btn sm" id="beAdmin">اجرای مجدد با دسترسی مدیر</button></div>` : ''}
  <form id="sform" onsubmit="return false">
  <div class="section"><h2>اتصال هوشمند</h2><div class="panel pad">
    ${switchRow('autoFailover', s.autoFailover, 'تعویض خودکار سرور', 'اگر سرور فعلی سه بار پشت سر هم جواب نداد، بهترین سرور بعدی جایگزین می‌شود')}
    ${switchRow('connectOnStart', s.connectOnStart, 'اتصال خودکار هنگام باز شدن برنامه', '')}
    <div class="win-only">${switchRow('autoStart', s.autoStart, 'اجرا همراه ویندوز', '')}</div>
  </div></div>
  <div class="section"><h2>پروکسی و TUN</h2><div class="panel pad">
    <div class="grid3">
      ${inp('mixedPort', 'پورت محلی SOCKS/HTTP', 1, 'number')}
      ${sel('tunStack', 'پشته TUN', [['mixed', 'mixed (پیشنهادی)'], ['system', 'system'], ['gvisor', 'gvisor']])}
      ${inp('tunMtu', 'MTU کارت TUN', 1, 'number')}
    </div>
    <div class="win-only">${switchRow('setSystemProxy', s.setSystemProxy, 'تنظیم پروکسی ویندوز در حالت پروکسی', 'مرورگرها و برنامه‌هایی که از پروکسی سیستم پیروی می‌کنند')}</div>
    ${switchRow('allowLan', s.allowLan, 'اشتراک با دستگاه‌های شبکه', 'گوشی و لپ‌تاپ دیگر می‌توانند از پورت بالا به‌عنوان پروکسی استفاده کنند')}
    ${switchRow('strictRoute', s.strictRoute, 'مسیریابی سخت‌گیرانه در TUN', 'جلوی نشت DNS و ترافیک خارج از تونل را می‌گیرد')}
    ${switchRow('sniff', s.sniff, 'تشخیص دامنه از ترافیک (sniff)', 'برای مسیریابی دقیق‌تر و یادگیری سایت‌ها لازم است')}
    ${switchRow('fakeIp', s.fakeIp, 'FakeIP در حالت TUN', 'سرعت بیشتر در باز شدن سایت‌ها؛ با برخی برنامه‌ها ناسازگار است')}
    ${switchRow('ipv6', s.ipv6, 'IPv6', '')}
  </div></div>
  <div class="section"><h2>DNS</h2><div class="panel pad"><div class="grid2">
    ${inp('remoteDns', 'DNS از داخل تونل', 1, '', 'مثل https://1.1.1.1/dns-query یا tls://8.8.8.8')}
    ${inp('directDns', 'DNS مستقیم', 1, '', 'برای سایت‌های ایرانی و تشخیص فیلتر؛ مثل 8.8.8.8 یا local')}
  </div></div></div>
  <div class="section"><h2>ضد شناسایی</h2><div class="panel pad">
    ${switchRow('fragment', s.fragment, 'تکه‌تکه کردن TLS (record fragment)', 'برای سرورهای TLS بدون Reality، دست دادن TLS را برای عبور از DPI تکه می‌کند')}
    ${switchRow('mux', s.mux, 'Multiplex (h2mux)', 'چند اتصال در یک اتصال؛ سرور هم باید پشتیبانی کند')}
  </div></div>
  <div class="section"><h2>تست سرورها</h2><div class="panel pad"><div class="grid3">
    ${inp('testUrl', 'آدرس تست', 1)}
    ${inp('testTimeoutMs', 'مهلت (میلی‌ثانیه)', 1, 'number')}
    ${inp('testConcurrency', 'تست هم‌زمان', 1, 'number')}
  </div></div></div>
  <div class="section"><h2>به‌روزرسانی</h2><div class="panel pad">
    <div class="grid2">
      ${inp('githubMirror', 'میرور GitHub (اختیاری)', 1, '', 'پیشوندی که قبل از لینک دانلود اضافه می‌شود، مثل https://ghfast.top/')}
      ${inp('appRepo', 'مخزن آپدیت برنامه', 1, '', 'owner/repo')}
    </div>
    ${switchRow('updateViaProxy', s.updateViaProxy, 'دانلود آپدیت‌ها از داخل تونل', 'وقتی وصل هستید، دانلود موتورها و برنامه از طریق تونل انجام می‌شود')}
    ${switchRow('checkUpdates', s.checkUpdates, 'بررسی خودکار نسخه جدید', '')}
  </div></div>
  <div class="section"><h2>ظاهر و گزارش</h2><div class="panel pad"><div class="grid2">
    ${sel('theme', 'پوسته', [['dark', 'تیره'], ['light', 'روشن']])}
    ${sel('logLevel', 'سطح گزارش موتورها', [['error', 'فقط خطا'], ['warn', 'هشدار'], ['info', 'اطلاعات'], ['debug', 'جزئیات کامل']])}
  </div></div></div>
  <div class="section win-only"><h2>اکستنشن مرورگر</h2><div class="panel pad">
    ${switchRow('extApi', s.extApi, 'اجازه اتصال اکستنشن رهگذر', 'فقط روی همین کامپیوتر (127.0.0.1) و فقط برای اکستنشن‌هایی که خودتان تأیید کنید')}
    <div class="grid2" style="margin-top:8px">
      ${inp('extPort', 'پورت ارتباط با اکستنشن', 1, 'number')}
      <div class="field"><span>وضعیت</span><div style="display:flex;gap:8px;align-items:center;height:38px">${s.extToken ? '<span class="badge ok">اکستنشن جفت شده</span><button class="btn sm danger" type="button" id="revokeExt">قطع دسترسی همه اکستنشن‌ها</button>' : '<span class="badge">هنوز جفت نشده</span>'}</div></div>
    </div>
    <p class="mute" style="font-size:12.5px;margin:12px 0 0">برای نصب: در Chrome یا Edge صفحه <span class="ltr">chrome://extensions</span> را باز کنید، «Developer mode» را روشن کنید و پوشه <b>extension</b> کنار Rahgozar.exe را با «Load unpacked» اضافه کنید. سپس در اکستنشن «اتصال به رهگذر» را بزنید و اینجا تأیید کنید.</p>
    <div style="margin-top:10px"><button class="btn sm" type="button" id="openExt">${icon('folder')}باز کردن پوشه اکستنشن</button></div>
  </div></div>
  <div class="section win-only"><h2>ابزار ویندوز</h2><div class="panel pad">
    <div class="switch-row"><div class="txt"><b>رفع مشکل L2TP پشت مودم (NAT)</b><small>کلید AssumeUDPEncapsulationContextOnSendRule را تنظیم می‌کند؛ نیاز به دسترسی مدیر و ری‌استارت</small></div><button class="btn sm" type="button" id="fixL2tp">اعمال</button></div>
  </div></div>
  </form>`;
};

Pages.settingsMount = function (v) {
  const f = v.querySelector('#sform');
  const nums = ['mixedPort', 'tunMtu', 'testTimeoutMs', 'testConcurrency', 'extPort'];
  f.addEventListener('change', e => {
    const el = e.target;
    if (!el.name) return;
    let val = el.type === 'checkbox' ? el.checked : el.value;
    if (nums.includes(el.name)) val = Number(val) || 0;
    saveSettings({ [el.name]: val });
  });
  v.querySelector('#openData').onclick = () => API.call('OpenDataDir');
  v.querySelector('#openExt').onclick = () => API.call('OpenExtensionDir').catch(e => toast(errText(e), 'error'));
  const rv = v.querySelector('#revokeExt');
  if (rv) rv.onclick = async () => {
    if (!await confirmBox('قطع دسترسی اکستنشن‌ها', 'همه اکستنشن‌های جفت‌شده دسترسی‌شان را از دست می‌دهند و باید دوباره جفت شوند.', 'قطع دسترسی', true)) return;
    await API.call('RevokeExtensions'); S.st = await API.call('GetState'); render();
  };
  const ba = v.querySelector('#beAdmin'); if (ba) ba.onclick = needAdmin;
  v.querySelector('#fixL2tp').onclick = async () => { try { await API.call('FixL2TP'); toast('اعمال شد؛ ویندوز را ری‌استارت کنید', 'ok'); } catch (e) { toast(errText(e), 'error'); } };
};

// =====================================================================
// LOGS
// =====================================================================
function logLineHTML(l) {
  const d = new Date(l.t);
  const t = d.toTimeString().slice(0, 8);
  return `<div class="logline ${l.lvl}"><span class="t">${t}</span> <span class="s">[${esc(l.src)}]</span> ${esc(l.text)}</div>`;
}
function connRows() {
  const list = (S.traffic && S.traffic.list) || [];
  return list.map(c => `<tr><td class="h">${esc(c.host)}</td><td>${c.outbound === 'direct' ? '<span class="badge ok">مستقیم</span>' : '<span class="badge info">تونل</span>'}</td><td class="ltr mute">${esc(c.process || '')}</td><td class="num">${bytesStr(c.down)}</td></tr>`).join('') || '<tr><td colspan="4" class="mute">اتصالی نیست</td></tr>';
}
Pages.logs = function () {
  const f = S.logFilter || 'all';
  const lines = S.logs.filter(l => f === 'all' || l.lvl === f || (f === 'warn' && l.lvl === 'error'));
  return `
  <div class="page-head"><div><h1>گزارش‌ها</h1><p>خروجی موتورها، تصمیم‌های یادگیری و اتصال‌های زنده.</p></div>
    <div class="actions"><button class="btn" id="copyLogs">${icon('copy')}کپی</button><button class="btn danger" id="clearLogs">${icon('trash')}پاک کردن</button></div></div>
  <div class="tabs"><button data-ltab="logs" aria-selected="${S.logTab === 'logs'}">گزارش</button><button data-ltab="conns" aria-selected="${S.logTab === 'conns'}">اتصال‌های زنده</button></div>
  ${S.logTab === 'logs' ? `
    <div class="toolbar"><div class="seg">${[['all', 'همه'], ['warn', 'هشدار و خطا'], ['error', 'خطا']].map(([k, t]) => `<button data-lf="${k}" aria-pressed="${f === k}">${t}</button>`).join('')}</div></div>
    <div class="logbox scroll" id="logbox">${lines.slice(-800).map(logLineHTML).join('')}</div>` :
    `<div class="panel" style="overflow:auto"><table class="domain-table conn-table"><thead><tr><th>مقصد</th><th>مسیر</th><th>برنامه</th><th>دانلود</th></tr></thead><tbody id="connBody">${connRows()}</tbody></table></div>`}`;
};
Pages.logsMount = function (v) {
  const box = v.querySelector('#logbox');
  if (box) box.scrollTop = box.scrollHeight;
  v.querySelectorAll('[data-ltab]').forEach(b => b.onclick = () => { S.logTab = b.dataset.ltab; render(); });
  v.querySelectorAll('[data-lf]').forEach(b => b.onclick = () => { S.logFilter = b.dataset.lf; render(); });
  v.querySelector('#clearLogs').onclick = async () => { await API.call('ClearLogs'); S.logs = []; render(); };
  v.querySelector('#copyLogs').onclick = async () => { await API.call('CopyText', S.logs.map(l => new Date(l.t).toISOString() + ' [' + l.src + '] ' + l.text).join('\n')); toast('کپی شد', 'ok'); };
};
Pages.logsAppend = function (lines) {
  const box = document.getElementById('logbox');
  if (!box) return;
  const f = S.logFilter || 'all';
  const stick = box.scrollTop + box.clientHeight > box.scrollHeight - 30;
  box.insertAdjacentHTML('beforeend', lines.filter(l => f === 'all' || l.lvl === f || (f === 'warn' && l.lvl === 'error')).map(logLineHTML).join(''));
  while (box.childElementCount > 1200) box.firstElementChild.remove();
  if (stick) box.scrollTop = box.scrollHeight;
};
Pages.connsUpdate = function () { const b = document.getElementById('connBody'); if (b) b.innerHTML = connRows(); };

// =====================================================================
// ANDROID: per-app tunnel
// =====================================================================
function isAndroid() { return (S.st && S.st.os === 'android') || API.android; }
const ANDROID_UNSUPPORTED = ['pptp', 'l2tp', 'sstp', 'ikev2', 'ssr'];

function perAppSection(s) {
  const mode = s.perAppMode || 'off';
  const n = (s.perAppList || []).length;
  const label = { off: 'همه برنامه‌ها از تونل می‌روند.', exclude: `همه برنامه‌ها به‌جز ${fa(n)} برنامه انتخاب‌شده از تونل می‌روند.`, include: `فقط ${fa(n)} برنامه انتخاب‌شده از تونل می‌روند.` }[mode];
  return `<div class="section"><h2>برنامه‌ها</h2><div class="panel pad">
    <div class="seg" role="group" aria-label="تونل برای برنامه‌ها">
      <button data-pam="off" aria-pressed="${mode === 'off'}">همه</button>
      <button data-pam="exclude" aria-pressed="${mode === 'exclude'}">به‌جز انتخاب‌شده‌ها</button>
      <button data-pam="include" aria-pressed="${mode === 'include'}">فقط انتخاب‌شده‌ها</button>
    </div>
    <p class="mute" style="margin:10px 0 12px;font-size:13px">${label} مثلاً برنامه‌های بانکی را در «به‌جز» بگذارید تا مستقیم وصل شوند.</p>
    <button class="btn" id="pickApps" ${mode === 'off' ? 'disabled' : ''}>${icon('servers')}انتخاب برنامه‌ها</button>
  </div></div>`;
}

async function openAppPicker() {
  let apps = [];
  const chosen = new Set(S.st.settings.perAppList || []);
  let showSys = false, q = '';
  const listHTML = () => apps
    .filter(a => (showSys || !a.system || chosen.has(a.pkg)) && (!q || (a.name + ' ' + a.pkg).toLowerCase().includes(q)))
    .sort((a, b) => (chosen.has(b.pkg) - chosen.has(a.pkg)) || a.name.localeCompare(b.name, 'fa'))
    .slice(0, 400)
    .map(a => `<label class="app-row"><input type="checkbox" class="check" data-pkg="${esc(a.pkg)}" ${chosen.has(a.pkg) ? 'checked' : ''}><span class="nm">${esc(a.name)}<small class="ltr">${esc(a.pkg)}</small></span></label>`).join('')
    || '<div class="empty">برنامه‌ای پیدا نشد</div>';
  Modal.open(`<div class="dialog" role="dialog" aria-modal="true"><header><h3>انتخاب برنامه‌ها</h3></header><div class="body">
    <div class="toolbar"><div class="search grow">${icon('search')}<input class="input" id="aq" placeholder="جستجو"></div>
      <label class="app-row" style="border:0;padding:0"><input type="checkbox" class="check" id="sys">برنامه‌های سیستمی</label></div>
    <div class="list-pick" id="al"><div class="empty">در حال خواندن فهرست برنامه‌ها…</div></div></div>
    <footer><button class="btn primary" data-ok>ذخیره (${'<span id="ac">' + fa(chosen.size) + '</span>'})</button><button class="btn ghost" data-no>انصراف</button></footer></div>`, async m => {
    const al = m.querySelector('#al');
    try { apps = (await API.call('AndroidApps')) || []; } catch (e) { al.innerHTML = `<div class="empty">${esc(errText(e))}</div>`; return; }
    al.innerHTML = listHTML();
    m.querySelector('#aq').oninput = e => { q = e.target.value.trim().toLowerCase(); al.innerHTML = listHTML(); };
    m.querySelector('#sys').onchange = e => { showSys = e.target.checked; al.innerHTML = listHTML(); };
    al.onchange = e => {
      const pkg = e.target.dataset.pkg;
      if (!pkg) return;
      e.target.checked ? chosen.add(pkg) : chosen.delete(pkg);
      m.querySelector('#ac').textContent = fa(chosen.size);
    };
    m.querySelector('[data-no]').onclick = Modal.close;
    m.querySelector('[data-ok]').onclick = async () => { Modal.close(); await saveSettings({ perAppList: [...chosen] }); render(); toast('ذخیره شد', 'ok'); };
  });
}
