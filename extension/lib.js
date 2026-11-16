// Shared client for the Rahgozar local API (used by background, popup, options).
/* global chrome */
const RG = {
  defaults: { port: 47123, token: '', browserProxy: false, siteScope: 'site' },

  async cfg() {
    const c = await chrome.storage.local.get(RG.defaults);
    return Object.assign({}, RG.defaults, c);
  },
  async setCfg(patch) { await chrome.storage.local.set(patch); },

  async req(path, opts) {
    const c = await RG.cfg();
    opts = opts || {};
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), opts.timeout || 8000);
    let resp;
    try {
      resp = await fetch(`http://127.0.0.1:${c.port}${path}`, {
        method: opts.body ? 'POST' : (opts.method || 'GET'),
        headers: Object.assign({ 'Content-Type': 'application/json' }, c.token ? { 'X-Rahgozar-Token': c.token } : {}),
        body: opts.body ? JSON.stringify(opts.body) : undefined,
        signal: ctrl.signal,
        cache: 'no-store'
      });
    } catch (e) {
      const err = new Error('برنامه رهگذر در دسترس نیست. آن را باز کنید.');
      err.code = 'offline';
      throw err;
    } finally { clearTimeout(timer); }
    let data = {};
    try { data = await resp.json(); } catch (e) { /* empty */ }
    if (resp.status === 401) { const e = new Error('اکستنشن هنوز به رهگذر وصل (جفت) نشده است.'); e.code = 'unpaired'; throw e; }
    if (!resp.ok) { const e = new Error(data.error || ('HTTP ' + resp.status)); e.code = 'api'; throw e; }
    return data;
  },

  status: () => RG.req('/api/status'),
  toggle: () => RG.req('/api/toggle', { body: {}, timeout: 15000 }),
  connect: () => RG.req('/api/connect', { body: {}, timeout: 15000 }),
  disconnect: () => RG.req('/api/disconnect', { body: {}, timeout: 15000 }),
  servers: () => RG.req('/api/servers'),
  select: id => RG.req('/api/select', { body: { id } }),
  mode: mode => RG.req('/api/mode', { body: { mode } }),
  routing: mode => RG.req('/api/routing', { body: { mode } }),
  site: host => RG.req('/api/site?host=' + encodeURIComponent(host)),
  setSite: (host, action, scope) => RG.req('/api/site', { body: { host, action, scope: scope || 'site' } }),
  probe: host => RG.req('/api/site/probe', { body: { host }, timeout: 30000 }),
  testAll: () => RG.req('/api/test', { body: {} }),

  async pair(onState) {
    const r = await RG.req('/api/pair/request', { body: { name: RG.browserName() } });
    const deadline = Date.now() + 120000;
    while (Date.now() < deadline) {
      await new Promise(res => setTimeout(res, 1200));
      const p = await RG.req('/api/pair/poll?id=' + encodeURIComponent(r.id));
      if (onState) onState(p.state);
      if (p.state === 'approved') { await RG.setCfg({ token: p.token }); return true; }
      if (p.state === 'denied' || p.state === 'expired') return false;
    }
    return false;
  },

  browserName() {
    const ua = navigator.userAgent;
    if (/Edg\//.test(ua)) return 'Microsoft Edge';
    if (/OPR\//.test(ua)) return 'Opera';
    if (/Brave/.test(ua)) return 'Brave';
    if (/Chrome\//.test(ua)) return 'Google Chrome';
    return 'Browser';
  },

  hostOf(url) {
    try {
      const u = new URL(url);
      if (!/^https?:$/.test(u.protocol)) return '';
      return u.hostname.replace(/^www\./, '');
    } catch (e) { return ''; }
  },

  // Browser-only proxy: route just this browser through Rahgozar's local port.
  async applyBrowserProxy(on, port) {
    if (on && port) {
      await chrome.proxy.settings.set({
        scope: 'regular',
        value: {
          mode: 'fixed_servers',
          rules: {
            singleProxy: { scheme: 'socks5', host: '127.0.0.1', port: port },
            bypassList: ['localhost', '127.0.0.1', '::1', '<local>']
          }
        }
      });
    } else {
      await chrome.proxy.settings.clear({ scope: 'regular' });
    }
  }
};

const ROUTE_FA = { proxy: 'از تونل', direct: 'مستقیم', block: 'مسدود' };
const RULE_FA = { proxy: 'همیشه تونل', direct: 'همیشه مستقیم', block: 'مسدود', auto: 'خودکار' };
