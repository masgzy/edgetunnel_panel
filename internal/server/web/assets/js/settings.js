// 设置逻辑
import { initPage, injectNav, injectAppBar, api, theme, toast, ttoast, copyText, escapeHtml, url, confirmDialog, i18n, gatewayPort, apiError } from '/assets/js/m3e.js';

async function init() {
  injectAppBar('设置');
  injectNav('settings');
  initPage();
  highlightThemeChip();
  bindThemeChips();
  bindSeedPicker();
  bindConfigCopy();
  bindConfigSave();
  bindConfigReload();
  bindRestartService();
  bindBackup();
  bindRestore();
  bindRefreshLogs();
  bindSCConfig();
  bindGeneralSettings();
  bindGenSettings();
  bindFullConfig();
  await Promise.all([loadSCConfig(), loadGeneralSettings(), loadGenSettings(), loadConfigFile(), loadLogs(), loadFullConfig()]);
  connectLogWS();
  // 关于页：版本号从 /api/status 动态取（与 CLI --version 同源），不再硬编码
  try {
    const st = await api('/api/status');
    if (st.version) document.getElementById('aboutVersion').textContent = st.version;
  } catch { /* 保持占位 */ }
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}

function highlightThemeChip() {
  const cur = theme.get();
  document.querySelectorAll('.theme-chip').forEach(chip => {
    chip.classList.toggle('selected', chip.dataset.themeMode === cur);
  });
}

function bindThemeChips() {
  document.querySelectorAll('.theme-chip').forEach(chip => {
    chip.addEventListener('click', () => {
      theme.set(chip.dataset.themeMode);
      highlightThemeChip();
    });
  });
}

// ---- 主题色（seed 动态色板，HCT 由 theme.bundle 提供） ----
function bindSeedPicker() {
  const grid = document.getElementById('seedGrid');
  if (!grid) return;
  const swatches = grid.querySelectorAll('.seed-swatch');
  const custom = document.getElementById('seedCustom');
  const reset = document.getElementById('seedReset');

  const cur = window.EDTColor?.current?.() || '#6750A4';
  if (custom) custom.value = cur;
  const apply = (hex) => {
    window.EDTColor?.applySeed?.(hex);
    swatches.forEach(sw => sw.classList.toggle('selected', sw.dataset.seed.toLowerCase() === hex.toLowerCase()));
    toast(i18n.t('t_seed_ok'));
  };
  // 初始高亮（默认紫不高亮：等于无自定义状态）
  swatches.forEach(sw => {
    if (cur.toLowerCase() === '#6750a4') return;
    sw.classList.toggle('selected', sw.dataset.seed.toLowerCase() === cur.toLowerCase());
  });
  swatches.forEach(sw => sw.addEventListener('click', () => apply(sw.dataset.seed)));
  custom?.addEventListener('input', (e) => apply(e.target.value));
  reset?.addEventListener('click', () => {
    window.EDTColor?.reset?.();
    swatches.forEach(sw => sw.classList.remove('selected'));
    if (custom) custom.value = '#6750A4';
    toast(i18n.t('t_seed_ok'));
  });
}

// ---- subconverter 配置 ----
async function loadSCConfig() {
  try {
    const st = await api('/api/status');
    const sc = st.subconverter || {};
    document.getElementById('scModeSelect').value = sc.mode || 'off';
    document.getElementById('scPortInput').value = sc.local_port || 25500;
    // 加载安装状态和远程地址列表
    const cfg = await api('/api/sc-config');
    document.getElementById('scInstallStatus').textContent = cfg.installed
      ? i18n.t('sc_installed') + ': ' + cfg.bin_path
      : i18n.t('sc_not_installed');
    // 填充远程地址下拉
    const sel = document.getElementById('scRemoteSelect');
    sel.innerHTML = '<option value="">-- 选择远程后端 --</option>';
    (cfg.remotes || []).forEach(r => {
      const opt = document.createElement('option');
      opt.value = r.value;
      opt.textContent = r.label;
      sel.appendChild(opt);
    });
    if (sc.remote) {
      sel.value = sc.remote;
      document.getElementById('scRemoteInput').value = sc.remote;
    }
    updateSCFields();
    document.getElementById('scModeSelect').addEventListener('change', updateSCFields);
  } catch (err) {
    document.getElementById('scInstallStatus').textContent = '查询失败';
  }
}

function updateSCFields() {
  const mode = document.getElementById('scModeSelect').value;
  document.getElementById('scRemoteField').style.display = mode === 'remote' ? '' : 'none';
  document.getElementById('scLocalField').style.display = mode === 'local' ? '' : 'none';
}

function bindSCConfig() {
  document.getElementById('scSaveBtn').addEventListener('click', async () => {
    const mode = document.getElementById('scModeSelect').value;
    const remoteVal = document.getElementById('scRemoteInput').value.trim()
      || document.getElementById('scRemoteSelect').value;
    const port = parseInt(document.getElementById('scPortInput').value) || 25500;
    try {
      await api('/api/sc-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mode, remote: remoteVal, port }),
      });
      ttoast('t_config_saved');
    } catch (err) {
      ttoast('t_config_save_fail', err.message);
    }
  });

  document.getElementById('scInstallBtn').addEventListener('click', async () => {
    const btn = document.getElementById('scInstallBtn');
    btn.disabled = true;
    btn.textContent = '下载中…';
    const threads = parseInt(document.getElementById('scThreads').value) || 32;
    document.getElementById('scInstallStatus').textContent = `下载中（${threads} 线程）…`;
    try {
      const resp = await fetch(url('/api/sc-install'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ threads }),
      });
      const result = await resp.json();
      if (resp.ok) {
        document.getElementById('scInstallStatus').textContent = '✓ 安装成功';
        ttoast('t_sc_installed');
      } else {
        document.getElementById('scInstallStatus').textContent = '✗ ' + (result.error || '安装失败');
        ttoast('t_sc_install_fail');
      }
    } catch (err) {
      document.getElementById('scInstallStatus').textContent = '✗ ' + err.message;
      ttoast('t_sc_install_fail', err.message);
    }
    btn.disabled = false;
    btn.innerHTML = '<span class="material-symbols-rounded">download</span>下载安装';
  });
}

// ---- 通用设置 ----
async function loadGeneralSettings() {
  try {
    const st = await api('/api/status');
    const rt = st.runtime || {};
    document.getElementById('settingsSubPort').value = rt.subscription_port || 8443;
    document.getElementById('settingsB64').checked = rt.encode_base64 || false;
    // 填充默认模板下拉
    const sel = document.getElementById('settingsProfile');
    sel.innerHTML = '';
    (rt.profiles || []).forEach(p => {
      const opt = document.createElement('option');
      opt.value = p.key;
      opt.textContent = p.name + ' (' + p.key + ')';
      if (p.key === rt.default_profile) opt.selected = true;
      sel.appendChild(opt);
    });
    // 读取 localStorage 偏好
    document.getElementById('settingsHistoryLimit').value = localStorage.getItem('edt-history-limit') || 10;
    document.getElementById('settingsLogLines').value = localStorage.getItem('edt-log-lines') || 50;
  } catch (err) {
    // 静默
  }
}

function bindGeneralSettings() {
  document.getElementById('settingsSaveBtn').addEventListener('click', async () => {
    const subPort = parseInt(document.getElementById('settingsSubPort').value) || 8443;
    const profile = document.getElementById('settingsProfile').value;
    const b64 = document.getElementById('settingsB64').checked;
    const historyLimit = parseInt(document.getElementById('settingsHistoryLimit').value) || 10;
    const logLines = parseInt(document.getElementById('settingsLogLines').value) || 50;
    // 保存偏好到 localStorage
    localStorage.setItem('edt-history-limit', historyLimit);
    localStorage.setItem('edt-log-lines', logLines);
    // 保存配置到 config.yml
    try {
      await api('/api/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          subscription_port: subPort,
          default_profile: profile,
          encode_base64: b64,
          history_limit: historyLimit,
          log_lines: logLines,
        }),
      });
      ttoast('t_config_saved');
    } catch (err) {
      ttoast('t_config_save_fail', err.message);
    }
  });
}

// ---- 生成配置（协议与设置） ----
let genAutoOn = true;

function genReadManualFromUI() {
  return {
    protocol: document.getElementById('genProtocol').value,
    transport: document.getElementById('genTransport').value,
    grpc_mode: document.getElementById('genGrpcMode').value,
    grpc_user_agent: document.getElementById('genGrpcUA').value.trim(),
    skip_cert_verify: document.getElementById('genSkipCert').checked,
    enable_0rtt: document.getElementById('gen0rtt').checked,
    fragment: document.getElementById('genFragment').value,
    random_path: document.getElementById('genRandomPath').checked,
    ech: document.getElementById('genECH').checked,
    ech_dns: document.getElementById('genEchDNS').value.trim(),
    ech_sni: document.getElementById('genEchSNI').value.trim(),
    fingerprint: document.getElementById('genFingerprint').value,
  };
}

function genFillManualUI(gs) {
  const g = gs || {};
  document.getElementById('genProtocol').value = g.protocol || 'vless';
  document.getElementById('genTransport').value = g.transport || 'ws';
  document.getElementById('genGrpcMode').value = g.grpc_mode || 'gun';
  document.getElementById('genGrpcUA').value = g.grpc_user_agent || '';
  document.getElementById('genSkipCert').checked = !!g.skip_cert_verify;
  document.getElementById('gen0rtt').checked = g.enable_0rtt !== false;
  document.getElementById('genFragment').value = g.fragment || '';
  document.getElementById('genRandomPath').checked = !!g.random_path;
  document.getElementById('genECH').checked = !!g.ech;
  document.getElementById('genEchDNS').value = g.ech_dns || '';
  document.getElementById('genEchSNI').value = g.ech_sni || '';
  document.getElementById('genFingerprint').value = g.fingerprint || 'chrome';
  updateGenRows();
}

function updateGenRows() {
  const transport = document.getElementById('genTransport').value;
  document.getElementById('genGrpcRow').hidden = transport !== 'grpc';
  document.getElementById('genEchRow').hidden = !document.getElementById('genECH').checked;
}

function renderGenPanelChips(panelGS, hosts, ok) {
  const box = document.getElementById('genPanelChips');
  const statusEl = document.getElementById('genPanelStatus');
  const hostsEl = document.getElementById('genPanelHosts');
  if (!ok || !panelGS) {
    box.innerHTML = '';
    statusEl.className = 'body-small mt-2 text-on-surface-variant';
    statusEl.textContent = i18n.t('gen_panel_unavailable');
    hostsEl.textContent = '';
    return;
  }
  statusEl.textContent = '';
  const labels = {
    protocol: i18n.t('gen_protocol'),
    transport: i18n.t('gen_transport'),
    fingerprint: i18n.t('gen_fingerprint'),
    fragment: i18n.t('gen_fragment'),
  };
  const values = [
    [labels.protocol, panelGS.protocol || '-'],
    [labels.transport, panelGS.transport || '-'],
    [labels.fingerprint, panelGS.fingerprint || '-'],
    [labels.fragment, panelGS.fragment ? panelGS.fragment : i18n.t('gen_frag_off')],
  ];
  if (panelGS.proxy_path && panelGS.proxy_path.path_tpl) {
    const pp = panelGS.proxy_path;
    const proxyVal = (pp.proxy_ip && pp.proxy_ip !== 'auto') ? pp.proxy_ip + ' · ' : '';
    values.push([i18n.t('gen_proxy_path'), proxyVal + pp.path_tpl]);
  }
  box.innerHTML = values.map(([k, v]) =>
    `<span class="chip">${escapeHtml(k)}: ${escapeHtml(v)}</span>`
  ).join('') + buildSwitchChips(panelGS);
  hostsEl.textContent = hosts && hosts.length ? i18n.t('gen_panel_hosts') + ': ' + hosts.join(', ') : '';
}

function buildSwitchChips(g) {
  const labels = { skip: i18n.t('gen_skip_cert'), rtt: '0-RTT', rnd: i18n.t('gen_random_path'), ech: 'ECH' };
  return Object.entries({
    skip: g.skip_cert_verify,
    rtt: g.enable_0rtt,
    rnd: g.random_path,
    ech: g.ech,
  }).filter(([, on]) => on).map(([key]) => `<span class="chip">${labels[key]}</span>`).join('');
}

async function refreshGenPanel(then) {
  try {
    const resp = await api('/api/gen', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh: true }),
    });
    if (resp.panel_ok) ttoast('t_config_saved');
    else toast(i18n.t('gen_panel_unavailable'));
    then && then(resp);
  } catch (err) {
    ttoast('t_config_save_fail', err.message);
  }
}

async function loadGenSettings() {
  try {
    const st = await api('/api/gen');
    genAutoOn = st.auto !== false;
    document.getElementById('genAuto').checked = genAutoOn;
    document.getElementById('genAgg').checked = !!st.aggregate;
    genFillManualUI(st.manual || st.effective || {});
    // 面板快照展示：接口未返回 panel 时，若 auto 开启尝试刷新一次
    if (st.panel) {
      renderGenPanelChips(st.panel, st.panel_hosts, true);
      toggleGenManualHint(true);
    } else {
      renderGenPanelChips(null, null, false);
      if (st.need_restart !== false && genAutoOn) {
        // 订阅首次请求会自动拉面板；此处静默等待即可
        toggleGenManualHint(false);
      }
    }
    updateGenSourceUI(genAutoOn);
  } catch (err) {
    // 保持默认 UI
  }
}

function toggleGenManualHint(panelOk) {
  const el = document.getElementById('genPanelStatus');
  if (!panelOk && !el.textContent) el.textContent = i18n.t('gen_panel_unavailable');
}

// 自动模式隐藏手动表单；关闭则展开手动设置。
function updateGenSourceUI(auto) {
  document.getElementById('genManualBox').hidden = !!auto;
  const panelBox = document.getElementById('genPanelBox');
  panelBox.style.display = auto ? '' : 'none';
}

function bindGenSettings() {
  document.getElementById('genAuto').addEventListener('change', (e) => {
    genAutoOn = e.target.checked;
    updateGenSourceUI(genAutoOn);
  });
  document.getElementById('genTransport').addEventListener('change', updateGenRows);
  document.getElementById('genECH').addEventListener('change', updateGenRows);
  document.getElementById('genRefreshBtn')?.addEventListener('click', async () => {
    await refreshGenPanel(async () => {
      const st = await api('/api/gen');
      renderGenPanelChips(st.panel, st.panel_hosts, st.panel_ok);
    });
  });
  document.getElementById('genSaveBtn').addEventListener('click', async () => {
    try {
      const body = { auto: document.getElementById('genAuto').checked };
      body.aggregate = document.getElementById('genAgg').checked;
      if (!body.auto) body.manual = genReadManualFromUI();
      await api('/api/gen', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      ttoast('t_config_saved');
    } catch (err) {
      ttoast('t_config_save_fail', err.message);
    }
  });
}

// ---- 配置文件编辑 ----
async function loadConfigFile() {
  try {
    const text = await api('/api/configfile');
    document.getElementById('configViewer').value = text;
  } catch (err) {
    document.getElementById('configViewer').value = '加载失败：' + err.message;
  }
}

function bindConfigCopy() {
  document.getElementById('copyConfigBtn').addEventListener('click', () => {
    const text = document.getElementById('configViewer').value;
    if (!text || text === '加载中…') return ttoast('t_config_not_loaded');
    copyText(text);
  });
}

function bindConfigSave() {
  document.getElementById('saveConfigBtn').addEventListener('click', async () => {
    const text = document.getElementById('configViewer').value;
    const btn = document.getElementById('saveConfigBtn');
    btn.disabled = true;
    try {
      const resp = await fetch(url('/api/configfile'), {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain' },
        body: text,
      });
      const result = await resp.json().catch(() => ({}));
      if (resp.ok) {
        const needRestart = result.need_restart || [];
        if (needRestart.length) {
          // 少数监听参数无法热生效：提示重启（可点「重启服务」）
          toast(i18n.t('t_config_saved_partial').replace('{items}', needRestart.join(', ')));
        } else {
          toast(i18n.t('t_config_hot_ok'));
        }
      } else {
        // 校验失败：服务端拒绝写盘，展示具体错误行
        ttoast('t_config_invalid', result.error || `HTTP ${resp.status}`);
      }
    } catch (err) {
      ttoast('t_config_save_fail', err.message);
    }
    btn.disabled = false;
  });
}

// ---- 重启服务（优雅关闭后 exec 自身；Windows 返回错误提示） ----
function bindRestartService() {
  const btn = document.getElementById('restartPanelBtn');
  if (!btn) return;
  btn.addEventListener('click', async () => {
    const ok = await confirmDialog(i18n.t('cd_restart_title'), i18n.t('cd_restart_content'));
    if (!ok) return;
    btn.disabled = true;
    try {
      const resp = await api('/api/restart', { method: 'POST' });
      toast(resp.message || i18n.t('t_restarting'));
      // 轮询等待服务恢复（重启通常 1~2s；最长等 30s）
      const deadline = Date.now() + 30000;
      const probe = setInterval(async () => {
        try {
          await fetch(url('/api/status'), { cache: 'no-store' });
          clearInterval(probe);
          location.reload();
        } catch (e) {
          if (Date.now() > deadline) {
            clearInterval(probe);
            btn.disabled = false;
          }
        }
      }, 1500);
    } catch (err) {
      ttoast('t_restart_fail', err.message);
      btn.disabled = false;
    }
  });
}

function bindConfigReload() {
  document.getElementById('reloadConfigBtn').addEventListener('click', loadConfigFile);
}

// ---- 备份/恢复 ----
function bindBackup() {
  document.getElementById('backupBtn').addEventListener('click', () => {
    window.location.href = url('/api/backup');
    ttoast('t_backup_start');
  });
}

function bindRestore() {
  document.getElementById('restoreBtn').addEventListener('click', () => {
    document.getElementById('restoreFile').click();
  });
  document.getElementById('restoreFile').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;
    const ok = await confirmDialog(i18n.t('cd_restore_title'), i18n.t('cd_restore_content').replace('{name}', file.name));
    if (!ok) { e.target.value = ''; return; }
    const fd = new FormData();
    fd.append('file', file);
    try {
      const resp = await fetch(url('/api/restore'), { method: 'POST', body: fd });
      const result = await resp.json();
      if (!resp.ok) {
        // 服务端拒绝（zip 非法/鉴权过期等）时不再误报成功
        throw new Error(result?.error || `HTTP ${resp.status}`);
      }
      toast(i18n.t('t_restore_ok') + result.restored + (result.failed > 0 ? i18n.t('t_with_fail').replace('{n}', result.failed) : ''));
      setTimeout(() => location.reload(), 1500);
    } catch (err) {
      ttoast('t_restore_fail', err.message);
    }
    e.target.value = '';
  });
}

// ---- 日志 ----
async function loadLogs() {
  try {
    const text = await api('/api/logs');
    document.getElementById('logViewer').value = text || '(无日志)';
    const viewer = document.getElementById('logViewer');
    viewer.scrollTop = viewer.scrollHeight;
  } catch (err) {
    document.getElementById('logViewer').value = '加载失败：' + err.message;
  }
}

function bindRefreshLogs() {
  document.getElementById('refreshLogsBtn').addEventListener('click', loadLogs);
}

// WebSocket 实时日志（edt_panel 自身 /ws 端点）
let logWS = null;
function connectLogWS() {
  const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${wsProto}//${location.host}/ws`;
  try {
    logWS = new WebSocket(wsUrl);
    logWS.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === 'init') {
          const viewer = document.getElementById('logViewer');
          if (msg.data) {
            viewer.value = msg.data;
            viewer.scrollTop = viewer.scrollHeight;
          }
        } else if (msg.type === 'append') {
          const viewer = document.getElementById('logViewer');
          viewer.value += msg.data;
          viewer.scrollTop = viewer.scrollHeight;
        }
      } catch (e) {}
    };
    logWS.onclose = () => {
      setTimeout(connectLogWS, 5000);
    };
    logWS.onerror = () => { logWS.close(); };
  } catch (e) {}
}

// ==================== 完整配置（config.yml 全节结构化设置） ====================
// 后端 /api/config-sections：GET 返回各节磁盘值；POST 合并保存（校验+备份+热重载）。
// 面板此前只有 subconverter/订阅端口/生成配置三处图形化设置，其余项都得手改 YAML；
// 此卡片把 app 之外的常改项全部暴露出来（app.host/port 修改需重启，故只读展示）。

let fcData = null; // GET 结果缓存（by_region 行渲染用）

function bindFullConfig() {
  document.getElementById('fcReloadBtn').addEventListener('click', loadFullConfig);
  document.getElementById('fcSaveBtn').addEventListener('click', saveFullConfig);
}

async function loadFullConfig() {
  const body = document.getElementById('fcBody');
  try {
    fcData = await api('/api/config-sections');
    fcRender(fcData);
  } catch (err) {
    body.innerHTML = `<span class="body-small text-error">${escapeHtml('加载失败：' + err.message)}</span>`;
  }
}

// fcText 生成一个填充文本框
function fcText(id, labelKey, value, attrs = '') {
  return `
    <div class="text-field filled">
      <label for="${id}">${escapeHtml(i18n.t(labelKey))}</label>
      <input id="${id}" type="text" value="${escapeHtml(value ?? '')}" ${attrs}>
    </div>`;
}

// fcNum 数字输入
function fcNum(id, labelKey, value) {
  return `
    <div class="text-field filled">
      <label for="${id}">${escapeHtml(i18n.t(labelKey))}</label>
      <input id="${id}" type="number" value="${Number(value) || 0}">
    </div>`;
}

// fcSwitch 开关行（m3e-switch 与 genAuto 同款）
function fcSwitch(id, labelKey, checked) {
  return `
    <label class="gen-auto-row text-field">
      <m3e-switch id="${id}" ${checked ? 'checked' : ''} aria-label="${escapeHtml(i18n.t(labelKey))}"></m3e-switch>
      <span>${escapeHtml(i18n.t(labelKey))}</span>
    </label>`;
}

// fcGroup 分组标题
function fcGroup(titleKey) {
  return `<div class="label-large text-primary mt-4" style="margin-bottom:8px;">${escapeHtml(i18n.t(titleKey))}</div>`;
}

function fcRender(d) {
  const body = document.getElementById('fcBody');
  const parts = [];

  // 远程对接
  parts.push(fcGroup('fc_group_remote'));
  parts.push(`<div class="flex gap-2" style="flex-wrap:wrap;">`);
  parts.push(fcText('fcControlDomain', 'fc_control_domain', d.remote.control_domain));
  parts.push(fcText('fcAdminURL', 'fc_admin_url', d.remote.admin_url));
  parts.push(fcNum('fcRequestTimeout', 'fc_request_timeout', d.remote.request_timeout));
  parts.push(fcNum('fcSubPort', 'fc_sub_port', d.remote.subscription_port));
  parts.push(fcNum('fcUUIDTTL', 'fc_uuid_ttl', d.remote.uuid_cache_ttl));
  parts.push(`</div>`);

  // 鉴权与口令
  parts.push(fcGroup('fc_group_auth'));
  parts.push(`<div class="flex gap-2" style="flex-wrap:wrap;">`);
  parts.push(fcText('fcLoginPassword', 'fc_login_password', '', 'type="password" autocomplete="new-password" placeholder="' + escapeHtml(i18n.t('fc_leave_empty')) + '"'));
  parts.push(fcText('fcWebPassword', 'fc_web_password', '', 'type="password" autocomplete="new-password" placeholder="' + escapeHtml(i18n.t('fc_leave_empty')) + '"'));
  parts.push(fcText('fcUserinfoExpire', 'fc_userinfo_expire', d.auth.userinfo_expire));
  parts.push(`</div>`);
  parts.push(fcSwitch('fcWebEmpty', 'fc_web_empty', false));

  // 节点解析
  parts.push(fcGroup('fc_group_nodes'));
  parts.push(`
    <div class="text-field" style="max-width:280px;">
      <label for="fcBareRole">${escapeHtml(i18n.t('fc_bare_ip_role'))}</label>
      <select id="fcBareRole">
        <option value="proxyip" ${d.nodes.bare_ip_role !== 'yxip' ? 'selected' : ''}>proxyip</option>
        <option value="yxip" ${d.nodes.bare_ip_role === 'yxip' ? 'selected' : ''}>yxip</option>
      </select>
    </div>`);

  // 国旗补全
  parts.push(fcGroup('fc_group_flag'));
  parts.push(fcSwitch('fcFlagIATA', 'fc_flag_iata', d.flag.iata));
  parts.push(fcSwitch('fcFlagISO2', 'fc_flag_iso2', d.flag.iso2));

  // ProxyIP 兑底
  parts.push(fcGroup('fc_group_proxyip'));
  parts.push(`<div class="flex gap-2" style="flex-wrap:wrap;">`);
  parts.push(fcText('fcProxyGlobal', 'fc_proxy_global', d.proxyip.global));
  parts.push(`</div>`);
  parts.push(fcSwitch('fcProxyDetect', 'fc_proxy_detect', d.proxyip.detect));
  parts.push(`<div class="label-medium mt-2" style="margin-bottom:6px;">${escapeHtml(i18n.t('fc_by_region'))}</div>`);
  parts.push(`<div id="fcRegionRows"></div>`);
  parts.push(`<button class="btn btn-tonal mt-2" id="fcAddRegion"><span class="material-symbols-rounded">add</span>${escapeHtml(i18n.t('fc_add_region'))}</button>`);

  // 文件路径（只读）
  parts.push(fcGroup('fc_group_files'));
  parts.push(`<div class="flex gap-2" style="flex-wrap:wrap;">` +
    Object.entries(d.files || {}).map(([k, v]) =>
      `<span class="chip"><span>${escapeHtml(k)}</span><span class="chip-sub cell-mono">${escapeHtml(v)}</span></span>`
    ).join('') + `</div>`);

  // 应用（只读）
  parts.push(fcGroup('fc_group_app'));
  parts.push(`<div class="flex gap-2" style="flex-wrap:wrap;">
    <span class="chip"><span>host</span><span class="chip-sub cell-mono">${escapeHtml(d.app.host)}</span></span>
    <span class="chip"><span>port</span><span class="chip-sub cell-mono">${escapeHtml(String(d.app.port))}</span></span>
    <span class="chip"><span>debug</span><span class="chip-sub cell-mono">${d.app.debug ? 'true' : 'false'}</span></span>
  </div>`);

  body.innerHTML = parts.join('');
  fcRenderRegions(d.proxyip.by_region || {});
  document.getElementById('fcAddRegion').addEventListener('click', () => fcAddRegionRow('', ''));
}

// fcRenderRegions 渲染 by_region 键值行
function fcRenderRegions(map) {
  const wrap = document.getElementById('fcRegionRows');
  wrap.innerHTML = '';
  Object.entries(map).forEach(([code, val]) => fcAddRegionRow(code, val));
}

function fcAddRegionRow(code, val) {
  const wrap = document.getElementById('fcRegionRows');
  const row = document.createElement('div');
  row.className = 'flex gap-2 mt-1';
  row.style.alignItems = 'center';
  row.innerHTML = `
    <div class="text-field filled" style="max-width:140px;">
      <input type="text" class="fc-region-code" placeholder="${escapeHtml(i18n.t('fc_region_code'))}" value="${escapeHtml(code)}" style="text-transform:uppercase;">
    </div>
    <div class="text-field filled" style="flex:1;min-width:200px;">
      <input type="text" class="fc-region-val" placeholder="${escapeHtml(i18n.t('fc_region_value'))}" value="${escapeHtml(val)}">
    </div>
    <button class="icon-btn fc-region-del" aria-label="remove"><span class="material-symbols-rounded">close</span></button>`;
  row.querySelector('.fc-region-del').addEventListener('click', () => row.remove());
  wrap.appendChild(row);
}

async function saveFullConfig() {
  const btn = document.getElementById('fcSaveBtn');
  btn.disabled = true;
  try {
    const d = fcData || {};
    const payload = {
      remote: {
        control_domain: document.getElementById('fcControlDomain').value.trim(),
        admin_url: document.getElementById('fcAdminURL').value.trim(),
        request_timeout: parseInt(document.getElementById('fcRequestTimeout').value) || 15,
        subscription_port: parseInt(document.getElementById('fcSubPort').value) || 8443,
        uuid_cache_ttl: parseInt(document.getElementById('fcUUIDTTL').value) || 0,
      },
      auth: {},
      nodes: { bare_ip_role: document.getElementById('fcBareRole').value },
      flag: {
        iata: document.getElementById('fcFlagIATA').checked,
        iso2: document.getElementById('fcFlagISO2').checked,
      },
      proxyip: {
        global: document.getElementById('fcProxyGlobal').value.trim(),
        detect: document.getElementById('fcProxyDetect').checked,
        by_region: {},
      },
    };
    // 口令：仅在有输入时提交（避免误清）；web_password 勾选清空时提交空串
    const loginPw = document.getElementById('fcLoginPassword').value;
    if (loginPw) payload.auth.login_password = loginPw;
    const webPw = document.getElementById('fcWebPassword').value;
    const webEmpty = document.getElementById('fcWebEmpty').checked;
    if (webPw) payload.auth.web_password = webPw;
    else if (webEmpty) payload.auth.web_password = '';
    payload.auth.userinfo_expire = document.getElementById('fcUserinfoExpire').value.trim();

    document.querySelectorAll('#fcRegionRows .flex').forEach(row => {
      const code = row.querySelector('.fc-region-code').value.trim().toUpperCase();
      const val = row.querySelector('.fc-region-val').value.trim();
      if (code && val) payload.proxyip.by_region[code] = val;
    });

    const resp = await api('/api/config-sections', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const needRestart = resp.need_restart || [];
    if (needRestart.length) {
      toast((i18n.t('t_config_saved_partial') || '已保存，需重启生效：{items}').replace('{items}', needRestart.join(', ')));
    } else {
      toast(i18n.t('t_config_hot_ok') || i18n.t('t_config_saved'));
    }
    // 重新拉取磁盘值刷新只读 chips 与 by_region 行
    await loadFullConfig();
  } catch (err) {
    ttoast('t_config_save_fail', err.message);
  }
  btn.disabled = false;
}
