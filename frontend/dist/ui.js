// Small UI helpers shared by all pages.
const FA_DIGITS = '۰۱۲۳۴۵۶۷۸۹';
function fa(v) { return String(v).replace(/\d/g, d => FA_DIGITS[d]); }
function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
function bytes(n) {
  n = Number(n) || 0;
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return { v: n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1), u: u[i] };
}
function bytesStr(n) { const b = bytes(n); return fa(b.v) + ' ' + b.u; }
function speedParts(n) { const b = bytes(n); return { v: fa(b.v), u: b.u + '/s' }; }
function ago(ts) {
  if (!ts) return '—';
  const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
  if (s < 60) return 'همین حالا';
  if (s < 3600) return fa(Math.floor(s / 60)) + ' دقیقه پیش';
  if (s < 86400) return fa(Math.floor(s / 3600)) + ' ساعت پیش';
  return fa(Math.floor(s / 86400)) + ' روز پیش';
}
function duration(sec) {
  sec = Math.max(0, Math.floor(sec));
  const h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
  const p = n => String(n).padStart(2, '0');
  return fa((h ? h + ':' : '') + p(m) + ':' + p(s));
}
function latClass(ms) { if (!ms || ms <= 0) return 'na'; if (ms < 300) return 'good'; if (ms < 700) return 'mid'; return 'bad'; }

const PROTO_LABEL = {
  vmess: 'VMess', vless: 'VLESS', trojan: 'Trojan', shadowsocks: 'SS', ssr: 'SSR', socks: 'SOCKS', http: 'HTTP',
  hysteria: 'Hysteria', hysteria2: 'Hy2', tuic: 'TUIC', wireguard: 'WG', anytls: 'AnyTLS', ssh: 'SSH',
  openvpn: 'OpenVPN', pptp: 'PPTP', l2tp: 'L2TP', sstp: 'SSTP', ikev2: 'IKEv2', chain: 'Chain'
};
function protoSub(p) {
  if (['vmess', 'vless', 'trojan'].includes(p.type)) {
    const parts = [];
    if (p.network && p.network !== 'tcp') parts.push(p.network);
    if (p.security === 'reality') parts.push('reality');
    else if (p.security === 'tls') parts.push('tls');
    return parts.join('+');
  }
  return '';
}

const ICONS = {
  home: '<path d="M4 20V11C4 6.5 7.6 3 12 2c4.4 1 8 4.5 8 9v9"/><path d="M12 20v-7" stroke-dasharray="2 3"/>',
  servers: '<rect x="3" y="4" width="18" height="6" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7h.01M7 17h.01"/>',
  routing: '<path d="M6 21V9a3 3 0 0 1 3-3h9"/><path d="M15 3l3 3-3 3"/><path d="M6 13h6a3 3 0 0 1 3 3v5"/>',
  learn: '<path d="M12 3a6 6 0 0 0-3.5 10.9V17h7v-3.1A6 6 0 0 0 12 3z"/><path d="M9.5 21h5"/>',
  engines: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>',
  settings: '<path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0"/><circle cx="16" cy="6" r="2"/><circle cx="10" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>',
  logs: '<path d="M5 4h14v16H5z"/><path d="M8 8h8M8 12h8M8 16h5"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  clip: '<rect x="7" y="4" width="10" height="17" rx="2"/><path d="M9 4V3h6v1"/>',
  file: '<path d="M14 3H6v18h12V7z"/><path d="M14 3v4h4"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
  bolt: '<path d="M13 2L4 14h7l-1 8 9-12h-7z"/>',
  refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/>',
  copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V4H4v12h4"/>',
  qr: '<rect x="4" y="4" width="6" height="6" rx="1"/><rect x="14" y="4" width="6" height="6" rx="1"/><rect x="4" y="14" width="6" height="6" rx="1"/><path d="M14 14h2v2h-2zM18 18h2v2h-2zM14 18h2M18 14h2"/>',
  camera: '<path d="M4 8h3l2-3h6l2 3h3v11H4z"/><circle cx="12" cy="13" r="3.5"/>',
  screen: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
  image: '<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="2"/><path d="M21 17l-5-5-9 8"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/>',
  chev: '<path d="M15 6l-6 6 6 6"/>',
  shield: '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18"/>',
  spark: '<path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5L18 18M6 18l2.5-2.5M15.5 8.5L18 6"/>',
  download: '<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>',
  undo: '<path d="M9 14L4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 0 12h-3"/>',
  dup: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M4 16V4h12"/>',
  folder: '<path d="M3 6h7l2 2h9v11H3z"/>',
  upload: '<path d="M12 20V9M7 14l5-5 5 5M5 4h14"/>',
  chain: '<rect x="2" y="9" width="8" height="6" rx="3"/><rect x="14" y="9" width="8" height="6" rx="3"/><path d="M10 12h4"/>',
  tun: '<path d="M4 18c0-7 3.6-12 8-12s8 5 8 12"/><path d="M2 18h20"/>',
  proxy: '<circle cx="6" cy="12" r="2.5"/><circle cx="18" cy="12" r="2.5"/><path d="M8.5 12h7"/>'
};
const FLAG_OK = /^[a-z]{2}$/;
function flag(cc, cls) {
  if (!cc || !FLAG_OK.test(cc)) return `<span class="flag none ${cls || ''}" title="کشور نامشخص"></span>`;
  return `<img class="flag ${cls || ''}" src="flags/${cc}.png" alt="${cc.toUpperCase()}" title="${cc.toUpperCase()}" loading="lazy">`;
}
function icon(name) { return `<svg viewBox="0 0 24 24" aria-hidden="true">${ICONS[name] || ''}</svg>`; }

function toast(text, kind) {
  const box = document.getElementById('toasts');
  const el = document.createElement('div');
  el.className = 'toast ' + (kind || 'info');
  el.textContent = text;
  box.appendChild(el);
  setTimeout(() => el.remove(), kind === 'error' ? 8000 : 4200);
}

function errText(e) {
  if (!e) return 'خطای نامشخص';
  if (typeof e === 'string') return e;
  return e.message || String(e);
}

// ---------- modal ----------
const Modal = {
  open(html, onMount) {
    const m = document.getElementById('modal');
    m.innerHTML = html;
    m.hidden = false;
    m.onclick = ev => { if (ev.target === m) Modal.close(); };
    document.addEventListener('keydown', Modal._esc);
    if (onMount) onMount(m);
    const f = m.querySelector('[autofocus]') || m.querySelector('input, textarea, button');
    if (f) f.focus();
    return m;
  },
  close() {
    const m = document.getElementById('modal');
    m.hidden = true;
    m.innerHTML = '';
    document.removeEventListener('keydown', Modal._esc);
  },
  _esc(e) { if (e.key === 'Escape') Modal.close(); }
};

function confirmBox(title, text, okLabel, danger) {
  return new Promise(res => {
    Modal.open(`<div class="dialog sm" role="dialog" aria-modal="true"><header><h3>${esc(title)}</h3></header>
      <div class="body"><p class="lead">${esc(text)}</p></div>
      <footer><button class="btn ${danger ? 'danger' : 'primary'}" data-ok>${esc(okLabel || 'تأیید')}</button><button class="btn ghost" data-no>انصراف</button></footer></div>`,
      m => {
        m.querySelector('[data-ok]').onclick = () => { Modal.close(); res(true); };
        m.querySelector('[data-no]').onclick = () => { Modal.close(); res(false); };
      });
  });
}

function sw(name, checked) {
  return `<label class="switch"><input type="checkbox" name="${name}" ${checked ? 'checked' : ''}><i></i></label>`;
}
function switchRow(name, checked, title, sub) {
  return `<div class="switch-row"><div class="txt"><b>${title}</b>${sub ? `<small>${sub}</small>` : ''}</div>${sw(name, checked)}</div>`;
}

// Sparkline chart for speed history.
function drawSpark(canvas, a, b) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth, h = canvas.clientHeight;
  if (!w) return;
  canvas.width = w * dpr; canvas.height = h * dpr;
  const ctx = canvas.getContext('2d');
  ctx.scale(dpr, dpr);
  const css = getComputedStyle(document.documentElement);
  const max = Math.max(1024 * 64, ...a, ...b) * 1.15;
  const grid = css.getPropertyValue('--line-soft').trim();
  ctx.strokeStyle = grid; ctx.lineWidth = 1;
  for (let i = 1; i < 4; i++) { const y = Math.round(h * i / 4) + .5; ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke(); }
  const line = (arr, color, fill) => {
    if (arr.length < 2) return;
    const step = w / (arr.length - 1);
    ctx.beginPath();
    arr.forEach((v, i) => { const x = w - i * step, y = h - 4 - (v / max) * (h - 10); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
    ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.lineJoin = 'round'; ctx.stroke();
    if (fill) {
      ctx.lineTo(w - (arr.length - 1) * step, h); ctx.lineTo(w, h); ctx.closePath();
      const g = ctx.createLinearGradient(0, 0, 0, h);
      g.addColorStop(0, color + '44'); g.addColorStop(1, color + '00');
      ctx.fillStyle = g; ctx.fill();
    }
  };
  line(a, css.getPropertyValue('--firouzeh').trim(), true);
  line(b, css.getPropertyValue('--saffron').trim(), false);
}
