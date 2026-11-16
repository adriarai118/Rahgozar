/* global chrome, RG */
const $ = s => document.querySelector(s);

async function show() {
  const c = await RG.cfg();
  $('#port').value = c.port;
  $('#token').value = c.token;
  try {
    const st = await RG.status();
    $('#st').innerHTML = `<span class="route proxy">متصل به برنامه</span> <span class="mute">رهگذر نسخه <span class="ltr">${st.version}</span></span>`;
  } catch (e) {
    $('#st').innerHTML = `<span class="route ${e.code === 'unpaired' ? 'direct' : 'block'}">${e.code === 'unpaired' ? 'جفت نشده' : 'برنامه در دسترس نیست'}</span> <span class="mute">${e.message}</span>`;
  }
}

$('#save').onclick = async () => {
  const port = parseInt($('#port').value, 10) || 47123;
  await RG.setCfg({ port, token: $('#token').value.trim() });
  chrome.runtime.sendMessage({ type: 'refresh' });
  show();
};
$('#unpair').onclick = async () => { await RG.setCfg({ token: '' }); chrome.runtime.sendMessage({ type: 'refresh' }); show(); };
$('#pair').onclick = async () => {
  $('#pair').disabled = true;
  $('#st').textContent = 'منتظر تأیید داخل برنامه رهگذر…';
  try {
    const ok = await RG.pair();
    $('#st').textContent = ok ? 'متصل شد.' : 'درخواست رد شد یا زمانش تمام شد.';
  } catch (e) { $('#st').textContent = e.message; }
  $('#pair').disabled = false;
  chrome.runtime.sendMessage({ type: 'refresh' });
  setTimeout(show, 600);
};
show();
