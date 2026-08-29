// 仪表盘逻辑
import { initPage, injectNav, injectAppBar, api, toast, ttoast, copyText, confirmDialog, theme, escapeHtml, url, absoluteUrl, i18n, apiError } from '/assets/js/m3e.js';

// ACL4SSR 规则集数据
const ACL4SSR_RULES = [
  ['默认版 分组比较全', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online.ini'],
  ['更多去广告', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_AdblockPlus.ini'],
  ['多国分组', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_MultiCountry.ini'],
  ['无自动测速', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_NoAuto.ini'],
  ['无广告拦截规则', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_NoReject.ini'],
  ['精简版', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Mini.ini'],
  ['精简版 更多去广告', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Mini_AdblockPlus.ini'],
  ['精简版 不带自动测速', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Mini_NoAuto.ini'],
  ['精简版 带故障转移', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Mini_Fallback.ini'],
  ['精简版 自动测速故障转移负载均衡', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Mini_MultiMode.ini'],
  ['精简版 带港美日国家', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Mini_MultiCountry.ini'],
  ['全分组 重度用户使用', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Full.ini'],
  ['全分组 多模式', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Full_MultiMode.ini'],
  ['全分组 无自动测速', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Full_NoAuto.ini'],
  ['全分组 更多去广告', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Full_AdblockPlus.ini'],
  ['全分组 奈飞全量', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Full_Netflix.ini'],
  ['全分组 谷歌细分', 'https://raw.githubusercontent.com/ACL4SSR/ACL4SSR/refs/heads/master/Clash/config/ACL4SSR_Online_Full_Google.ini'],
];

// 需 subconverter 的格式
const SC_FORMATS = ['clash', 'clashr', 'surge', 'quanx', 'loon', 'singbox'];

let scEnabled = false;

async function init() {
  injectAppBar('仪表盘');
  injectNav('dashboard');
  initPage();
  fillACL4SSR();
  bindButtons();
  bindStatusSave();
  await loadStatus();
  updateGenUrl();
  renderHistory();
  restoreFromShare();
  // 每 3 秒刷新统计（只刷 stats，避免重渲染整个状态卡）；
  // 页面切回前台立即刷一次，后台时浏览器节流无妨
  setInterval(refreshStats, 3000);
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) refreshStats();
  });
}

async function refreshStats() {
  try {
    const st = await api('/api/status');
    renderStats(st.stats || {});
  } catch { /* 静默 */ }
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}

function fillACL4SSR() {
  const sel = document.getElementById('genConfig');
  ACL4SSR_RULES.forEach(([label, url]) => {
    const opt = document.createElement('option');
    opt.value = url;
    opt.textContent = label;
    sel.appendChild(opt);
  });
}

async function loadStatus() {
  try {
    const st = await api('/api/status');
    const sc = st.subconverter || {};
    const rt = st.runtime || {};
    scEnabled = sc.mode && sc.mode !== 'off';
    // 只读视图
    document.getElementById('kvPort').textContent = rt.subscription_port ?? '-';
    document.getElementById('kvProfile').textContent = rt.default_profile ?? '-';
    document.getElementById('kvB64').textContent = rt.encode_base64 ? i18n.t('on') : i18n.t('off');
    const modeKey = 'mode_' + (sc.mode || 'off');
    document.getElementById('kvSc').textContent = i18n.t(modeKey);
    document.getElementById('statusSubtitle').textContent = scEnabled
      ? `subconverter ${i18n.t(modeKey)} · ${i18n.t('enabled')}`
      : i18n.t('native_only');
    // 编辑视图填充
    document.getElementById('editPort').value = rt.subscription_port || 8443;
    document.getElementById('editB64').value = String(rt.encode_base64 || false);
    document.getElementById('editSc').value = sc.mode || 'off';
    const editProfileSel = document.getElementById('editProfile');
    editProfileSel.innerHTML = '';
    (rt.profiles || []).forEach(p => {
      const opt = document.createElement('option');
      opt.value = p.key;
      opt.textContent = p.name;
      if (p.key === rt.default_profile) opt.selected = true;
      editProfileSel.appendChild(opt);
    });
    // 显示编辑按钮
    document.getElementById('editStatusBtn').hidden = false;
    // 模板与数据源列表
    renderProfiles(rt.profiles || [], rt.default_profile);
    renderDataSources(rt.data_sources || []);
    // 调用统计
    renderStats(st.stats || {});
  } catch {
    document.getElementById('kvSc').textContent = 'unknown';
  }
}

function renderStats(st) {
  setStatValue('statSub', st.sub ?? 0);
  setStatValue('statMihomo', st.mihomo ?? 0);
  setStatValue('statConvert', st.convert ?? 0);
  setStatValue('statGetIP', st.getip ?? 0);
  setStatValue('statGetUUID', st.getuuid ?? 0);
  document.getElementById('statUptime').textContent = formatUptime(st.uptime ?? 0);
  const sub = document.getElementById('statsSubtitle');
  if (st.last_sub) {
    sub.textContent = `最近订阅 ${st.last_sub}`;
  } else if (st.uptime != null) {
    sub.textContent = `已运行 ${formatUptime(st.uptime)}`;
  }
}

function setStatValue(id, val) {
  const el = document.getElementById(id);
  if (el.textContent != String(val)) {
    el.textContent = val;
    el.classList.remove('count-animate');
    void el.offsetWidth; // 触发重排
    el.classList.add('count-animate');
  }
}

function formatUptime(sec) {
  if (sec < 60) return sec + 's';
  if (sec < 3600) return Math.floor(sec/60) + 'm' + (sec%60) + 's';
  const h = Math.floor(sec/3600);
  const m = Math.floor((sec%3600)/60);
  return h + 'h' + m + 'm';
}

function renderProfiles(profiles, defaultKey) {
  const el = document.getElementById('profileChips');
  document.getElementById('profileCountLabel').textContent = `${profiles.length} 个`;
  if (profiles.length === 0) {
    el.innerHTML = '<span class="body-small text-on-surface-variant">' + i18n.t('empty_no_template') + '</span>';
    return;
  }
  el.innerHTML = profiles.map(p => `
    <span class="chip ${p.key === defaultKey ? 'selected' : ''}">
      <span>${escapeHtml(p.name)}</span>
      <span class="chip-sub">${escapeHtml(p.key)} · ${escapeHtml(p.format)} · ${escapeHtml(p.uuid_mode)}</span>
    </span>
  `).join('');
}

function renderDataSources(sources) {
  const el = document.getElementById('dsList');
  document.getElementById('dsCountLabel').textContent = i18n.t('count_nodes').replace('{n}', sources.length);
  if (sources.length === 0) {
    el.innerHTML = '<span class="body-small text-on-surface-variant">' + i18n.t('empty_no_ds') + '</span>';
    return;
  }
  el.innerHTML = sources.map(ds => `
    <div class="ds-item">
      <div class="ds-id">${escapeHtml(ds.id)}</div>
      <div class="ds-info">
        <div class="ds-name">${escapeHtml(ds.name)}</div>
        <div class="ds-kind">${escapeHtml(i18n.t('kind_' + ds.kind))}</div>
      </div>
    </div>
  `).join('');
}

function bindButtons() {
  // UUID
  document.getElementById('getUuidBtn').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      const uuid = await api('/getuuid');
      document.getElementById('uuidInput').value = uuid;
      ttoast('t_uuid_ok');
    } catch (err) {
      ttoast('t_uuid_fail', apiError(err));
    } finally {
      btn.disabled = false;
    }
  });
  document.getElementById('copyUuidBtn').addEventListener('click', () => {
    const v = document.getElementById('uuidInput').value;
    if (!v) return ttoast('t_no_uuid');
    copyText(v);
  });
  document.getElementById('clearUuidBtn').addEventListener('click', async () => {
    try {
      await api('/clearuuid');
      document.getElementById('uuidInput').value = '';
      ttoast('t_uuid_cleared');
    } catch (err) {
      ttoast('t_clear_fail', apiError(err));
    }
  });

  // IP
  document.getElementById('getIpBtn').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      const d = await api('/getip');
      renderIpInfo(d);
      ttoast('t_ip_ok');
    } catch (err) {
      document.getElementById('ipInfo').innerHTML = `<span class="text-error">${escapeHtml(err.message)}</span>`;
      ttoast('t_ip_fail', apiError(err));
    } finally {
      btn.disabled = false;
    }
  });
  document.getElementById('copyIpBtn').addEventListener('click', () => {
    const ip = document.getElementById('ipInfo').dataset.ip;
    if (!ip) return ttoast('t_no_ip');
    copyText(ip);
  });

  // 域名
  document.getElementById('getDomainBtn').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      const domain = await api('/getdomain?id=1&type=main_edt');
      document.getElementById('domainInput').value = domain;
      ttoast('t_domain_ok');
    } catch (err) {
      ttoast('t_domain_fail', apiError(err));
    } finally {
      btn.disabled = false;
    }
  });
  document.getElementById('copyDomainBtn').addEventListener('click', () => {
    const v = document.getElementById('domainInput').value;
    if (!v) return ttoast('t_no_domain');
    copyText(v);
  });

  // 订阅生成器
  ['genId', 'genType', 'genFormat', 'genConfig'].forEach(id => {
    document.getElementById(id).addEventListener('change', updateGenUrl);
  });
  document.getElementById('copyUrlBtn').addEventListener('click', () => {
    const url = document.getElementById('genUrl').value;
    if (!url) return ttoast('t_no_param');
    copyText(url);
    addHistory(url);
  });
  document.getElementById('openUrlBtn').addEventListener('click', () => {
    const url = document.getElementById('genUrl').value;
    if (!url) return ttoast('t_no_param');
    window.open(url, '_blank');
    addHistory(url);
  });
  document.getElementById('downloadUrlBtn').addEventListener('click', () => {
    const url = document.getElementById('genUrl').value;
    if (!url) return ttoast('t_no_param');
    addHistory(url);
    window.location.href = url;
  });
  document.getElementById('qrBtn').addEventListener('click', toggleQR);
  document.getElementById('shareBtn').addEventListener('click', shareSubscription);
  document.getElementById('clearHistoryBtn').addEventListener('click', async () => {
    const list = getHistory();
    if (list.length === 0) return ttoast('t_no_history');
    const ok = await confirmDialog(i18n.t('cd_clear_history_title'), i18n.t('cd_clear_history_content'));
    if (!ok) return;
    localStorage.removeItem(HISTORY_KEY);
    renderHistory();
    ttoast('t_history_cleared');
  });
}

// 仪表盘状态卡片编辑/保存
function bindStatusSave() {
  const editBtn = document.getElementById('editStatusBtn');
  const saveBtn = document.getElementById('saveStatusBtn');
  const cancelBtn = document.getElementById('cancelStatusBtn');
  if (!editBtn) return;
  // 编辑按钮：切换到编辑视图
  editBtn.addEventListener('click', () => {
    document.getElementById('statusView').classList.add('hidden');
    document.getElementById('statusEdit').classList.remove('hidden');
    editBtn.hidden = true;
  });
  // 取消按钮：回到只读视图
  if (cancelBtn) {
    cancelBtn.addEventListener('click', () => {
      document.getElementById('statusView').classList.remove('hidden');
      document.getElementById('statusEdit').classList.add('hidden');
      editBtn.hidden = false;
    });
  }
  // 保存按钮
  if (saveBtn) {
    saveBtn.addEventListener('click', async () => {
      saveBtn.disabled = true;
      const subPort = parseInt(document.getElementById('editPort').value) || 8443;
      const profile = document.getElementById('editProfile').value;
      const b64 = document.getElementById('editB64').value === 'true';
      const scMode = document.getElementById('editSc').value;
      try {
        await api('/api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ subscription_port: subPort, default_profile: profile, encode_base64: b64 }),
        });
        await api('/api/sc-config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ mode: scMode }),
        });
        ttoast('t_config_saved');
        // 回到只读并刷新
        document.getElementById('statusView').classList.remove('hidden');
        document.getElementById('statusEdit').classList.add('hidden');
        editBtn.hidden = false;
        await loadStatus();
      } catch (err) {
        ttoast('t_config_save_fail', apiError(err));
      }
      saveBtn.disabled = false;
    });
  }
}

// 订阅历史（localStorage）
const HISTORY_KEY = 'edt-history';
const HISTORY_MAX = parseInt(localStorage.getItem("edt-history-limit")) || 10;

function getHistory() {
  try {
    return JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]');
  } catch { return []; }
}

function addHistory(rawUrl) {
  if (!rawUrl) return;
  const fullUrl = new URL(rawUrl, location.origin + location.pathname).href;
  const list = getHistory().filter(h => h.url !== fullUrl);
  list.unshift({ url: fullUrl, time: Date.now() });
  if (list.length > HISTORY_MAX) list.length = HISTORY_MAX;
  localStorage.setItem(HISTORY_KEY, JSON.stringify(list));
  renderHistory();
}

function renderHistory() {
  const el = document.getElementById('historyList');
  const list = getHistory();
  if (list.length === 0) {
    el.innerHTML = '<span class="body-small text-on-surface-variant">' + i18n.t('no_history') + '</span>';
    return;
  }
  el.innerHTML = list.map(h => {
    const t = new Date(h.time);
    const timeStr = t.getHours().toString().padStart(2,'0') + ':' + t.getMinutes().toString().padStart(2,'0');
    return `
      <div class="history-item">
        <span class="h-time">${timeStr}</span>
        <span class="h-url" title="${escapeHtml(h.url)}">${escapeHtml(h.url)}</span>
        <div class="h-actions">
          <button class="icon-btn" data-act="copy" data-url="${escapeHtml(h.url)}" title="复制"><span class="material-symbols-rounded">content_copy</span></button>
          <button class="icon-btn" data-act="open" data-url="${escapeHtml(h.url)}" title="打开"><span class="material-symbols-rounded">open_in_new</span></button>
        </div>
      </div>
    `;
  }).join('');
  el.querySelectorAll('[data-act="copy"]').forEach(b => {
    b.addEventListener('click', () => copyText(b.dataset.url));
  });
  el.querySelectorAll('[data-act="open"]').forEach(b => {
    b.addEventListener('click', () => window.open(b.dataset.url, '_blank'));
  });
}

function toggleQR() {
  const url = document.getElementById('genUrl').value;
  if (!url) return ttoast('t_no_param');
  const area = document.getElementById('qrArea');
  const codeEl = document.getElementById('qrCode');
  if (!area.classList.contains('hidden')) {
    // 已显示，隐藏
    area.classList.add('hidden');
    return;
  }
  // 生成完整 URL（相对路径转绝对）
  const fullUrl = new URL(url, location.origin + location.pathname).href;
  codeEl.innerHTML = '';
  // qrcodejs 全局 QRCode；库未加载/生成异常时降级为复制链接，避免抛错
  if (typeof QRCode === 'undefined') {
    copyText(fullUrl);
    area.classList.add('hidden');
    return;
  }
  try {
    // eslint-disable-next-line no-undef
    new QRCode(codeEl, {
      text: fullUrl,
      width: 200,
      height: 200,
      colorDark: '#000000',
      colorLight: '#ffffff',
      correctLevel: QRCode.CorrectLevel.M,
    });
  } catch (e) {
    copyText(fullUrl);
    area.classList.add('hidden');
    return;
  }
  area.classList.remove('hidden');
  ttoast('t_qr_ok');
}

// 分享订阅：把当前订阅参数编码成 base64 码，生成分享链接
function shareSubscription() {
  const url = document.getElementById('genUrl').value;
  if (!url) return ttoast('t_no_param');
  // 把订阅参数编码
  const fullUrl = new URL(url, location.origin + location.pathname).href;
  const code = btoa(encodeURIComponent(fullUrl));
  const shareUrl = location.origin + location.pathname + '?s=' + code;
  copyText(shareUrl);
  ttoast('t_share_ok');
}

// 从分享码还原订阅参数
function restoreFromShare() {
  const params = new URLSearchParams(location.search);
  const code = params.get('s');
  if (!code) return;
  try {
    const fullUrl = decodeURIComponent(atob(code));
    // 解析 fullUrl 的 query 参数填回生成器
    const u = new URL(fullUrl);
    const id = u.searchParams.get('id');
    const type = u.searchParams.get('type');
    const config = u.searchParams.get('config');
    if (id) document.getElementById('genId').value = id;
    if (type) document.getElementById('genType').value = type;
    if (config) document.getElementById('genConfig').value = config;
    // 根据 path 推断格式
    const path = u.pathname;
    if (path.includes('/mihomo')) document.getElementById('genFormat').value = 'mihomo';
    else if (path.includes('/convert')) {
      const target = u.searchParams.get('target');
      if (target) document.getElementById('genFormat').value = target;
    } else document.getElementById('genFormat').value = 'vless';
    updateGenUrl();
    ttoast('t_share_restore');
  } catch (e) {
    // 静默
  }
}

function renderIpInfo(d) {
  const el = document.getElementById('ipInfo');
  el.dataset.ip = d.ip || '';
  el.innerHTML = `
    <div class="ip-row"><span class="label">IP</span><span class="value mono">${escapeHtml(d.ip || '-')}</span></div>
    <div class="ip-row"><span class="label">${i18n.t('ip_send_recv')}</span><span class="value mono">${Number(d.send)||0} / ${Number(d.res)||0}</span></div>
    <div class="ip-row"><span class="label">${i18n.t('ip_plr')}</span><span class="value">${((Number(d.plr)||0)*100).toFixed(1)}%</span></div>
    <div class="ip-row"><span class="label">${i18n.t('ip_ping')}</span><span class="value mono">${Number(d.ping)||0} ms</span></div>
    <div class="ip-row"><span class="label">${i18n.t('ip_speed')}</span><span class="value mono">${(Number(d.speed)||0).toFixed(2)} MB/s</span></div>
    <div class="ip-row"><span class="label">${i18n.t('ip_region')}</span><span class="value">${escapeHtml(d.code || '-')}</span></div>
  `;
}

function updateGenUrl() {
  const id = document.getElementById('genId').value;
  const type = document.getElementById('genType').value;
  const fmt = document.getElementById('genFormat').value;
  const config = document.getElementById('genConfig').value;
  const configField = document.getElementById('configField');
  const scHint = document.getElementById('scHint');

  let rawUrl = '';
  const params = new URLSearchParams();
  if (id) params.set('id', id);
  if (type) params.set('type', type);

  if (fmt === 'vless') {
    rawUrl = '/sub?' + params.toString();
    configField.classList.add('hidden');
    scHint.classList.add('hidden');
  } else if (fmt === 'mihomo') {
    // mihomo 也支持选 ACL4SSR 规则集（后端据此生成对应 proxy-groups/rules）
    if (config) params.set('config', config);
    rawUrl = '/mihomo?' + params.toString();
    configField.classList.remove('hidden');
    scHint.classList.add('hidden');
  } else {
    // 走 subconverter
    params.set('target', fmt);
    if (config) params.set('config', config);
    rawUrl = '/convert?' + params.toString();
    configField.classList.remove('hidden');
    if (!scEnabled) {
      scHint.classList.remove('hidden');
    } else {
      scHint.classList.add('hidden');
    }
  }
  document.getElementById('genUrl').value = absoluteUrl(rawUrl);
}
