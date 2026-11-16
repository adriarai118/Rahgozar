// Bridge to the Go backend (Wails). When opened in a plain browser a mock
// backend is used so the interface can be previewed.
(function () {
  const real = !!(window.go && window.go.desk && window.go.desk.App);
  const listeners = {};

  function on(name, fn) {
    if (real) return window.runtime.EventsOn(name, fn);
    (listeners[name] = listeners[name] || []).push(fn);
  }
  function emit(name, data) { (listeners[name] || []).forEach(f => f(data)); }

  async function call(method, ...args) {
    if (real) return window.go.desk.App[method](...args);
    return mock[method] ? mock[method](...args) : null;
  }

  // ---------------- mock ----------------
  const now = () => Math.floor(Date.now() / 1000);
  const mk = (id, name, type, net, sec, server, port, eng, success, latency, samples, group) => ({
    id, name, type, network: net, security: sec, server, port, engineId: eng || 'sing-box', group: group || 'manual',
    family: [type === 'shadowsocks' ? 'ss' : type, net && net !== 'tcp' ? net : '', sec].filter(Boolean).join('+'),
    success, latency, samples, lastOk: now() - 300
  });
  const state = {
    version: '1.0.0', admin: false, enginesOk: true, os: 'windows', dataDir: 'C:\\Rahgozar\\data',
    settings: {
      language: 'fa', theme: 'dark', mode: 'proxy', routingMode: 'smart', selectedId: 'auto', autoSelect: true,
      autoFailover: true, learnDomains: true, mixedPort: 10808, allowLan: false, setSystemProxy: true, bypassLan: true,
      bypassIran: true, blockAds: false, remoteDns: 'https://1.1.1.1/dns-query', directDns: '8.8.8.8', fakeIp: false,
      tunStack: 'mixed', tunMtu: 9000, strictRoute: true, ipv6: false, sniff: true, testUrl: 'https://www.gstatic.com/generate_204',
      testTimeoutMs: 5000, testConcurrency: 16, githubMirror: '', updateViaProxy: true, appRepo: 'adriarai118/Rahgozar',
      checkUpdates: true, autoStart: false, connectOnStart: false, logLevel: 'warn', proxyDomains: ['youtube.com', 'instagram.com'],
      directDomains: ['aparat.com'], blockDomains: [], directProcesses: [], fragment: false, fragmentLen: '10-30', fragmentDelay: '10-20', mux: false
    },
    profiles: [
      mk('a1', 'آلمان ریلیتی', 'vless', 'tcp', 'reality', '91.107.10.20', 443, 'sing-box', .97, 142, 41, 's1'),
      mk('a2', 'هلند XHTTP', 'vless', 'xhttp', 'tls', 'nl.cdn-edge.net', 443, 'xray', .91, 188, 27, 's1'),
      mk('a3', 'فنلاند هیستریا', 'hysteria2', '', 'tls', 'fi.example.org', 8443, 'sing-box', .74, 121, 19, 's1'),
      mk('a4', 'ترکیه وی‌مس', 'vmess', 'ws', 'tls', 'tr.example.com', 2053, 'sing-box', .38, 402, 22, 's1'),
      mk('a5', 'Warp', 'wireguard', '', '', '162.159.192.1', 2408, 'sing-box', .12, 690, 9),
      mk('a6', 'OpenVPN شرکت', 'openvpn', '', '', 'vpn.company.ir', 1194, 'sing-box', 0, 0, 0),
      mk('a7', 'SSR قدیمی', 'ssr', '', '', '5.6.7.8', 8989, 'mihomo', .55, 310, 6),
      mk('a8', 'L2TP اداره', 'l2tp', '', '', 'l2tp.example.ir', 0, 'windows', 0, 0, 0),
      mk('a9', 'تروجان gRPC', 'trojan', 'grpc', 'tls', 'tj.example.net', 443, 'sing-box', .88, 205, 14, 's1'),
    ],
    subs: [{ id: 's1', name: 'اشتراک اصلی', url: 'https://sub.example.com/abc', autoUpdate: true, intervalH: 12, lastUpdate: now() - 3600, count: 5, viaProxy: true, download: 32e9, upload: 3e9, total: 100e9, expire: now() + 20 * 86400 }],
    status: { state: 'idle', mode: 'proxy', netLabel: 'Home-WiFi@Wi-Fi', admin: false, port: 10808 }
  };
  let timer = null;
  const mock = {
    GetState: async () => JSON.parse(JSON.stringify(state)),
    GetProfiles: async () => state.profiles,
    GetStatus: async () => state.status,
    SaveSettings: async s => { state.settings = s; },
    SetMode: async m => { if (m === 'tun' && !state.admin) throw 'NEED_ADMIN'; state.settings.mode = m; },
    SetRouting: async m => { state.settings.routingMode = m; },
    Select: async id => { state.settings.selectedId = id; },
    Connect: async () => {
      state.status = { ...state.status, state: 'connecting', message: 'انتخاب هوشمند بهترین سرور…' }; emit('status', state.status);
      await new Promise(r => setTimeout(r, 1400));
      const p = state.profiles[0];
      state.status = { ...state.status, state: 'connected', profileId: p.id, profileName: p.name, protocol: p.family, engine: 'sing-box', since: now(), latency: 138, autoChosen: true, message: '' };
      emit('status', state.status);
      let up = 0, down = 0;
      timer = setInterval(() => {
        const d = Math.round(200000 + Math.random() * 1600000), u = Math.round(20000 + Math.random() * 120000);
        up += u; down += d;
        emit('traffic', { up: u, down: d, upTotal: up, downTotal: down, conns: 12 + Math.round(Math.random() * 8), list: [
          { host: 'www.youtube.com', outbound: 'proxy', process: 'chrome.exe', up: 1200, down: 902100 },
          { host: 'digikala.com', outbound: 'direct', process: 'chrome.exe', up: 400, down: 12100 },
          { host: 'api.github.com', outbound: 'proxy', process: 'Code.exe', up: 3000, down: 8100 }] });
      }, 1000);
    },
    Disconnect: async () => { clearInterval(timer); state.status = { ...state.status, state: 'idle' }; emit('status', state.status); },
    ImportClipboard: async () => ({ added: 3, errors: [] }),
    ImportText: async () => ({ added: 1, errors: [] }),
    ImportFiles: async () => ({ added: 1, errors: [] }),
    SaveProfile: async p => { if (!p.id) { p.id = 'n' + Date.now(); state.profiles.push(p); } return p; },
    DeleteProfiles: async ids => { state.profiles = state.profiles.filter(p => !ids.includes(p.id)); },
    DuplicateProfile: async () => {},
    CopyLink: async () => {},
    TestProfiles: async ids => {
      emit('testStart', { ids });
      ids.forEach((id, i) => setTimeout(() => { const ok = Math.random() > .25; emit('test', { id, ok, ms: ok ? 90 + Math.round(Math.random() * 500) : 0, error: ok ? '' : 'timeout' }); if (i === ids.length - 1) emit('testDone'); }, 300 + i * 180));
    },
    AddSubscription: async (n, u) => ({ id: 's2', name: n || 'اشتراک', url: u, count: 4 }),
    UpdateSubscription: async () => 5, UpdateAllSubscriptions: async () => [], DeleteSubscription: async () => {}, SaveSubscription: async () => {},
    ListEngines: async () => [
      { id: 'sing-box', name: 'sing-box', repo: 'SagerNet/sing-box', kind: 'binary', required: true, desc: 'هسته اصلی: TUN، مسیریابی هوشمند، DNS و اکثر پروتکل‌ها', protocols: ['VMess', 'VLESS', 'Reality', 'Trojan', 'Shadowsocks', 'Hysteria2', 'TUIC', 'WireGuard', 'OpenVPN', 'TUN'], installed: true, version: '1.14.3', latest: '1.14.4', hasUpdate: true, hasBackup: true },
      { id: 'xray', name: 'Xray-core', repo: 'XTLS/Xray-core', kind: 'binary', desc: 'برای XHTTP، mKCP، VLESS Encryption و ترفندهای ضد فیلتر Xray', protocols: ['XHTTP', 'VLESS', 'Reality', 'mKCP'], installed: true, version: '26.3.27', latest: '26.3.27' },
      { id: 'mihomo', name: 'Mihomo (Clash.Meta)', repo: 'MetaCubeX/mihomo', kind: 'binary', desc: 'برای ShadowsocksR و سازگاری با اشتراک‌های Clash', protocols: ['ShadowsocksR', 'Clash'], installed: false },
      { id: 'rules-ir', name: 'قوانین ایران (Iran rules)', repo: 'Chocolate4U/Iran-sing-box-rules', kind: 'rules', required: true, desc: 'لیست سایت‌ها و IPهای ایرانی، تبلیغات و بدافزار برای مسیریابی هوشمند', protocols: ['geosite-ir', 'geoip-ir', 'ads'], installed: true, version: '202611121318', latest: '202611121318' }
    ],
    CheckEngine: async id => null, UpdateEngine: async () => {}, InstallEngineFromFile: async () => {}, RollbackEngine: async () => {}, UpdateAllEngines: async () => {},
    CheckAppUpdate: async () => ({ current: '1.0.0', latest: '1.0.0', available: false }),
    GetLearning: async () => ({
      netId: 'ab12', netLabel: 'Home-WiFi@Wi-Fi', samples: 412, direct: 184, proxy: 63, unknown: 9,
      families: [
        { family: 'vless+reality', success: .96, latency: 140, samples: 51, state: 'good' },
        { family: 'hysteria2', success: .78, latency: 120, samples: 23, state: 'good' },
        { family: 'trojan+grpc+tls', success: .62, latency: 210, samples: 14, state: 'weak' },
        { family: 'vmess+ws+tls', success: .35, latency: 400, samples: 22, state: 'weak' },
        { family: 'wireguard', success: .08, latency: 0, samples: 12, state: 'blocked' }],
      ranking: state.profiles.slice(0, 6).map((p, i) => ({ id: p.id, name: p.name, family: p.family, score: 1 - i * .14, success: p.success, latency: p.latency, samples: p.samples })),
      domains: [
        { domain: 'youtube.com', verdict: 'proxy', source: 'auto', reason: 'اتصال مستقیم قطع/مسدود شد: connection reset', hits: 812, checked: now() - 600 },
        { domain: 'google.com', verdict: 'direct', source: 'auto', reason: 'مستقیم در دسترس (180ms)', hits: 540, checked: now() - 900 },
        { domain: 'android.com', verdict: 'proxy', source: 'auto', reason: 'تحریم/محدودیت جغرافیایی (HTTP 403)', hits: 61, checked: now() - 1200 },
        { domain: 'x.com', verdict: 'proxy', source: 'auto', reason: 'DNS مسموم (10.10.34.36)', hits: 44, checked: now() - 100 },
        { domain: 'wikipedia.org', verdict: 'direct', source: 'user', reason: 'user', hits: 20, checked: now() - 3000 }]
    }),
    SetDomain: async () => {}, ResetLearning: async () => {},
    GetLogs: async () => [
      { t: Date.now() - 5000, src: 'app', text: 'connected: آلمان ریلیتی [vless+reality, proxy mode]', lvl: 'info' },
      { t: Date.now() - 3000, src: 'learn', text: 'youtube.com → proxy (connection reset)', lvl: 'info' },
      { t: Date.now() - 1000, src: 'sing-box', text: 'WARN [0012] outbound/vless[proxy]: handshake slow', lvl: 'warn' }],
    ClearLogs: async () => {}, CopyText: async () => {}, OpenDataDir: async () => {}, OpenURL: async () => {}, FixL2TP: async () => {},
    RelaunchAdmin: async () => {}, WindowMinimise: async () => {}, WindowToggleMaximise: async () => {}, Quit: async () => {}
  };

  window.API = { call, on, real };
})();
