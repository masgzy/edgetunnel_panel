// 优选 IP 逻辑
import { initPage, injectNav, injectAppBar, api, toast, ttoast, dialog, escapeHtml, i18n, apiError } from '/assets/js/m3e.js';

let data = [];
let isSortMode = false;
let selectedRow = null;
let currentPre = '';
let currentIp = '';
// coloMap 优选 IP -> 三字码（cfcolo）缓存；点「探测区域」后填充并渲染
const coloMap = new Map();

async function init() {
  injectAppBar(i18n.t('selector_title'));
  injectNav('selector');
  initPage();
  bindButtons();
  setupCustomDialog();
  await Promise.all([loadData(), loadCurrent()]);
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}

// 返回是否加载成功（供刷新按钮决定是否报成功提示）
async function loadData() {
  try {
    data = await api('/config?type=get');
    renderTable();
    const badge = document.getElementById('countBadge');
    badge.textContent = i18n.t('count_nodes').replace('{n}', data.length);
    badge.classList.toggle('success', data.length > 0);
    return true;
  } catch (err) {
    ttoast('t_load_fail', apiError(err));
    return false;
  }
}

// 返回是否读取当前优选成功
async function loadCurrent() {
  try {
    const text = await api('/api/preip?type=get');
    const lines = text.split('\n');
    currentPre = lines[0] || '';
    currentIp = lines[1] || '';
    document.getElementById('currentPre').textContent = currentPre || '-';
    document.getElementById('currentIp').textContent = currentIp || '-';
    renderTable(); // 重新渲染以高亮当前选中
    return true;
  } catch (e) {
    return false;
  }
}

function renderTable() {
  const tbody = document.getElementById('tableBody');
  tbody.innerHTML = '';
  if (data.length === 0) {
    tbody.innerHTML = `<tr><td colspan="6" class="text-center text-on-surface-variant" style="padding:48px;">${i18n.t('empty_no_nodes_selector')}</td></tr>`;
    return;
  }
  data.forEach((item, idx) => {
    const line = idx + 1;
    const ipToUse = item.yx_ip || item.ip;
    const isCurrent = ipToUse && ipToUse === currentIp;
    const colo = coloMap.get(ipToUse);
    const tr = document.createElement('tr');
    tr.dataset.line = line;
    if (isCurrent) tr.classList.add('current-row');
    tr.innerHTML = `
      <td data-label="${i18n.t('row_num')}" class="row-num">${line}</td>
      <td data-label="${i18n.t('proxy_ip')}" class="cell-mono">${escapeHtml(item.ip || 'DIRECT')}</td>
      <td data-label="${i18n.t('yx_ip')}" class="cell-mono">${escapeHtml(item.yx_ip || '-')}</td>
      <td data-label="${i18n.t('th_colo')}" class="colo-cell">${colo ? `<span class="badge success">${escapeHtml(colo)}</span>` : '<span class="text-on-surface-variant">—</span>'}</td>
      <td data-label="${i18n.t('name')}">${escapeHtml(item.name)}</td>
      <td data-label="${i18n.t('actions')}" class="action-cell">
        <button class="action-btn select ${isCurrent ? 'active' : ''}" data-act="select" ${isSortMode ? 'disabled' : ''} title="${i18n.t('set_current')}">
          <span class="material-symbols-rounded">${isCurrent ? 'check_circle' : 'radio_button_unchecked'}</span>
        </button>
      </td>
    `;
    if (isSortMode) {
      tr.style.cursor = 'pointer';
      tr.addEventListener('click', () => selectForSort(tr));
    } else {
      // 非排序模式：整行点击即可设为优选 IP（不只是点小圆圈）
      tr.style.cursor = 'pointer';
      tr.addEventListener('click', (e) => {
        // 点击操作按钮本身时不重复触发
        if (e.target.closest('.action-btn')) return;
        selectIp(line);
      });
    }
    tr.querySelector('[data-act="select"]').addEventListener('click', (e) => {
      e.stopPropagation();
      selectIp(line);
    });
    tbody.appendChild(tr);
  });
}

function bindButtons() {
  document.getElementById('refreshBtn').addEventListener('click', async () => {
    const [ok1, ok2] = await Promise.all([loadData(), loadCurrent()]);
    if (ok1 && ok2) ttoast('t_refreshed');
  });
  document.getElementById('sortBtn').addEventListener('click', toggleSort);
  // 探测区域：对全部行的优选 IP 提前发起 cdn-cgi/trace 探测并显示三字码
  // （无需等订阅请求，分区域 ProxyIP 兑底所依赖的区域码一目了然）
  document.getElementById('probeBtn').addEventListener('click', probeColo);
}

// probeColo 批量探测优选 IP 的 cfcolo（后端并发 8、进程内缓存 1h）
async function probeColo() {
  const btn = document.getElementById('probeBtn');
  if (btn.disabled) return;
  const hosts = [...new Set(
    data.map(it => (it.yx_ip || it.ip || '').trim())
        .filter(h => h && h !== 'DIRECT')
  )];
  if (hosts.length === 0) return ttoast('t_no_ip_node');
  btn.disabled = true;
  const label = btn.querySelector('span:last-child');
  const original = label.textContent;
  label.textContent = i18n.t('t_probe_running');
  try {
    const resp = await api('/api/probe-colo', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ hosts }),
    });
    let hit = 0;
    (resp.results || []).forEach(r => {
      if (r.code) {
        coloMap.set(r.host, r.code);
        hit++;
      }
    });
    renderTable();
    if (hit > 0) toast(i18n.t('colo_hint') + ` (${hit}/${hosts.length})`);
    else toast(i18n.t('t_probe_none'));
  } catch (err) {
    ttoast('t_probe_fail', apiError(err));
  }
  btn.disabled = false;
  label.textContent = original;
}

async function selectIp(line) {
  if (isSortMode) return;
  const item = data[line - 1];
  const ipToUse = item.yx_ip || item.ip;
  if (!ipToUse || ipToUse === 'DIRECT') return ttoast('t_no_ip_node');
  // 从 name 提取前缀
  let pre = item.name || 'default';
  if (pre.includes('-')) pre = pre.split('-')[0].trim();
  pre = pre + ' - ';
  try {
    await api(`/api/preip?type=set&pre=${encodeURIComponent(pre)}&ip=${encodeURIComponent(ipToUse)}`);
    currentPre = pre;
    currentIp = ipToUse;
    document.getElementById('currentPre').textContent = currentPre;
    document.getElementById('currentIp').textContent = currentIp;
    renderTable();
    ttoast('t_set_cur_ok', ipToUse);
  } catch (err) {
    ttoast('t_set_pre_fail', apiError(err));
  }
}

function toggleSort() { isSortMode ? exitSort() : enterSort(); }
function enterSort() {
  isSortMode = true; selectedRow = null;
  const btn = document.getElementById('sortBtn');
  btn.innerHTML = '<span class="material-symbols-rounded">close</span>退出排序';
  btn.classList.remove('btn-outline');
  btn.classList.add('btn-danger');
  ttoast('t_select_hint');
  renderTable();
}
function exitSort() {
  isSortMode = false; selectedRow = null;
  const btn = document.getElementById('sortBtn');
  btn.innerHTML = '<span class="material-symbols-rounded">swap_vert</span>排序';
  btn.classList.remove('btn-danger');
  btn.classList.add('btn-outline');
  renderTable();
}
function selectForSort(tr) {
  if (!isSortMode) return;
  if (!selectedRow) { selectedRow = tr; tr.classList.add('selected-row'); }
  else if (selectedRow === tr) { tr.classList.remove('selected-row'); selectedRow = null; }
  else {
    const l1 = parseInt(selectedRow.dataset.line);
    const l2 = parseInt(tr.dataset.line);
    swapRows(l1, l2);
    selectedRow.classList.remove('selected-row');
    selectedRow = null;
  }
}
async function swapRows(l1, l2) {
  if (l1 === l2) return;
  const fd = new FormData();
  fd.append('type', 'move');
  fd.append('line1', l1);
  fd.append('line2', l2);
  try {
    await api('/config', { method: 'POST', body: fd });
    ttoast('t_swap_ok');
    loadData();
  } catch (err) { ttoast('t_swap_fail', apiError(err)); exitSort(); }
}

function setupCustomDialog() {
  const d = document.getElementById('customDialog');
  const s = document.getElementById('customScrim');
  const ctrl = dialog(d, s);
  window._customCtrl = ctrl;
  document.getElementById('customBtn').addEventListener('click', () => {
    document.getElementById('customPre').value = currentPre;
    document.getElementById('customIp').value = currentIp;
    ctrl.open();
  });
  document.getElementById('customCancel').addEventListener('click', ctrl.close);
  document.getElementById('customOk').addEventListener('click', async () => {
    const pre = document.getElementById('customPre').value.trim();
    const ip = document.getElementById('customIp').value.trim();
    if (!pre || !ip) return ttoast('t_pre_ip_empty');
    try {
      await api(`/api/preip?type=set&pre=${encodeURIComponent(pre)}&ip=${encodeURIComponent(ip)}`);
      currentPre = pre; currentIp = ip;
      document.getElementById('currentPre').textContent = pre;
      document.getElementById('currentIp').textContent = ip;
      ctrl.close();
      renderTable();
      ttoast('t_set_ok');
    } catch (err) { ttoast('t_set_pre_fail', apiError(err)); }
  });
}
