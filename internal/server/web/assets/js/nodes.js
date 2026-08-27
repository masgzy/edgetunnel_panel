// 节点配置逻辑
import { initPage, injectNav, injectAppBar, api, toast, ttoast, confirmDialog, dialog, escapeHtml, gatewayPort, url, i18n, apiError } from '/assets/js/m3e.js';

let data = [];
let isSortMode = false;
let selectedRow = null;
let isEditing = false;
let refreshTimer = null;
let searchQuery = '';
let isSelectMode = false;
let selectedLines = new Set();

async function init() {
  injectAppBar('节点配置');
  injectNav('nodes');
  initPage();
  bindButtons();
  setupBatchDialog();
  setupImportDialog();
  setupBatchEditDialog();
  bindShortcuts();
  setupHelpDialog();
  setupFabScrollHide();
  await loadData();
  // 30 秒静默刷新
  refreshTimer = setInterval(silentRefresh, 30000);
}

/** FAB 滚动隐藏：向下滚动时收起 FAB，向上滚动或停下后 1.5s 重新出现，避免遮挡列表操作 */
function setupFabScrollHide() {
  const fab = document.getElementById('fabAdd');
  if (!fab) return;
  let lastY = window.scrollY;
  let hideTimer = null;
  window.addEventListener('scroll', () => {
    const y = window.scrollY;
    const goingDown = y > lastY && y - lastY > 4;
    lastY = y;
    if (goingDown) {
      fab.classList.add('fab-hide');
      clearTimeout(hideTimer);
    } else {
      clearTimeout(hideTimer);
      hideTimer = setTimeout(() => fab.classList.remove('fab-hide'), 600);
    }
  }, { passive: true });
  // 触摸抬起后 1.5s 显示回来
  window.addEventListener('touchend', () => {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => fab.classList.remove('fab-hide'), 1500);
  }, { passive: true });
}
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}

async function loadData() {
  try {
    data = await api('/config?type=get');
    renderTable();
    document.getElementById('lastUpdate').textContent = '更新于 ' + new Date().toLocaleTimeString();
  } catch (err) {
    document.getElementById('tableBody').innerHTML = `<tr><td colspan="5" class="text-center text-error" style="padding:32px;">${escapeHtml(err.message)}</td></tr>`;
    ttoast('t_load_fail', apiError(err));
  }
}

async function silentRefresh() {
  if (isSortMode || isEditing) return;
  try {
    data = await api('/config?type=get');
    renderTable();
  } catch (e) { /* 静默 */ }
}

function renderTable() {
  const tbody = document.getElementById('tableBody');
  tbody.innerHTML = '';
  // 搜索过滤（保留原始索引以便编辑/删除用正确 line 号）
  const filtered = [];
  data.forEach((item, idx) => {
    if (!searchQuery) {
      filtered.push({ item, line: idx + 1 });
      return;
    }
    const q = searchQuery.toLowerCase();
    if ((item.name || '').toLowerCase().includes(q)
      || (item.ip || '').toLowerCase().includes(q)
      || (item.yx_ip || '').toLowerCase().includes(q)) {
      filtered.push({ item, line: idx + 1 });
    }
  });
  // 更新计数 badge（0 个时移除 success 配色，避免空列表误显"成功"绿）
  const badge = document.getElementById('countBadge');
  const countText = searchQuery && filtered.length !== data.length
    ? `${filtered.length}/${data.length}`
    : `${data.length}`;
  badge.textContent = i18n.t('count_nodes').replace('{n}', countText);
  badge.classList.toggle('success', data.length > 0);
  if (filtered.length === 0) {
    tbody.innerHTML = `<tr><td colspan="5">
      <div class="empty-state">
        <span class="material-symbols-rounded">${searchQuery ? 'search_off' : 'dns'}</span>
        <div class="empty-title">${searchQuery ? i18n.t('empty_no_match') : i18n.t('empty_no_nodes')}</div>
      </div>
    </td></tr>`;
    return;
  }
  filtered.forEach(({ item, line }) => {
    const tr = document.createElement('tr');
    tr.dataset.line = line;
    const checkboxCell = isSelectMode
      ? `<td data-label="选择"><label class="check-cell"><input type="checkbox" data-line="${line}" ${selectedLines.has(line) ? 'checked' : ''}><span class="check-box"></span></label></td>`
      : `<td data-label="行号" class="row-num">${line}</td>`;
    tr.innerHTML = `
      ${checkboxCell}
      <td data-label="ProxyIP" class="cell-mono">${escapeHtml(item.ip || 'DIRECT')}</td>
      <td data-label="优选 IP" class="cell-mono">${escapeHtml(item.yx_ip || '-')}</td>
      <td data-label="名称">${escapeHtml(item.name)}</td>
      <td data-label="操作" class="action-cell">
        ${isSelectMode ? '' : `<button class="action-btn edit" data-act="edit" ${isSortMode ? 'disabled' : ''} title="编辑"><span class="material-symbols-rounded">edit</span></button>
        <button class="action-btn delete" data-act="del" ${isSortMode ? 'disabled' : ''} title="删除"><span class="material-symbols-rounded">delete</span></button>`}
      </td>
    `;
    if (isSelectMode) {
      const cb = tr.querySelector('input[type=checkbox]');
      cb.addEventListener('change', () => {
        if (cb.checked) selectedLines.add(line);
        else selectedLines.delete(line);
        updateSelectBar();
      });
    }
    if (isSortMode) {
      tr.style.cursor = 'move';
      tr.setAttribute('draggable', 'true');
      tr.addEventListener('click', () => selectForSort(tr));
      tr.addEventListener('dragstart', (e) => {
        tr.classList.add('dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', String(line));
      });
      tr.addEventListener('dragend', () => {
        tr.classList.remove('dragging');
        document.querySelectorAll('.data-table tr').forEach(t => t.classList.remove('drag-over', 'drag-before', 'drag-after'));
      });
      tr.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        // 清除其他行的指示
        document.querySelectorAll('.data-table tr').forEach(t => {
          t.classList.remove('drag-over', 'drag-before', 'drag-after');
        });
        // 根据鼠标 Y 位置判断插入上方/下方
        const rect = tr.getBoundingClientRect();
        const midY = rect.top + rect.height / 2;
        if (e.clientY < midY) {
          tr.classList.add('drag-before');
        } else {
          tr.classList.add('drag-after');
        }
      });
      tr.addEventListener('dragleave', () => {
        tr.classList.remove('drag-over', 'drag-before', 'drag-after');
      });
      tr.addEventListener('drop', (e) => {
        e.preventDefault();
        const fromLine = parseInt(e.dataTransfer.getData('text/plain'));
        // 判断插入位置
        const rect = tr.getBoundingClientRect();
        const midY = rect.top + rect.height / 2;
        const insertAfter = e.clientY >= midY;
        tr.classList.remove('drag-over', 'drag-before', 'drag-after');
        if (fromLine && fromLine !== line) {
          // 插入到目标行：before 用 line，after 用 line+1（后一行位置）
          const toLine = insertAfter ? line + 1 : line;
          // 若拖到下方且从上方拖来，目标行号 +1 已是下一行
          moveToLine(fromLine, toLine > 0 ? toLine : 1);
        }
      });
    }
    const editBtn = tr.querySelector('[data-act="edit"]');
    const delBtn = tr.querySelector('[data-act="del"]');
    if (editBtn) editBtn.addEventListener('click', (e) => { e.stopPropagation(); editRow(line); });
    if (delBtn) delBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      const ok = await confirmDialog(i18n.t('cd_del_title'), i18n.t('cd_del_content').replace('{line}', line).replace('{name}', item.name));
      if (!ok) return;
      const fd = new FormData();
      fd.append('type', 'del');
      fd.append('line', line);
      try {
        await api('/config', { method: 'POST', body: fd });
        ttoast('t_deleted');
        loadData();
      } catch (err) { ttoast('t_del_fail', apiError(err)); }
    });
    // 点击行打开详情抽屉（非排序/选择/编辑模式）
    if (!isSortMode && !isSelectMode) {
      tr.style.cursor = 'pointer';
      tr.addEventListener('click', (e) => {
        if (e.target.closest('button')) return; // 点按钮不触发
        openDetailDrawer(item, line);
      });
    }
    tbody.appendChild(tr);
  });
}

// 节点详情抽屉
function openDetailDrawer(item, line) {
  const content = document.getElementById('drawerContent');
  // 行号只读，其他字段可编辑
  const rows = [
    { label: '行号', val: line, editable: false },
    { label: '名称', val: item.name || '', editable: true, key: 'name' },
    { label: 'ProxyIP', val: item.ip || '', editable: true, key: 'ip' },
    { label: '优选 IP', val: item.yx_ip || '', editable: true, key: 'yx_ip' },
    { label: '优选 Host', val: item.yx_host || '', editable: false },
    { label: '优选 Port', val: item.yx_port || '', editable: false },
  ];
  content.innerHTML = rows.map(r => `
    <div class="detail-row">
      <span class="detail-label">${escapeHtml(r.label)}</span>
      ${r.editable
        ? `<input class="detail-input" data-key="${r.key}" value="${escapeHtml(r.val)}" placeholder="${escapeHtml(r.label)}">`
        : `<span class="detail-value">${escapeHtml(String(r.val))}</span>`}
    </div>
  `).join('');
  // 编辑按钮：切换到行内编辑模式（保留原 editRow）
  document.getElementById('drawerEdit').onclick = async () => {
    // 收集抽屉里编辑的字段
    const inputs = content.querySelectorAll('.detail-input');
    const updates = {};
    let hasChange = false;
    inputs.forEach(inp => {
      const k = inp.dataset.key;
      const v = inp.value.trim();
      const orig = (k === 'name' ? item.name : k === 'ip' ? (item.ip || '') : (item.yx_ip || '')) || '';
      if (v !== orig) {
        updates[k] = v;
        hasChange = true;
      }
    });
    if (!hasChange) {
      ttoast('t_no_change');
      return;
    }
    const fd = new FormData();
    fd.append('type', 'mod');
    fd.append('line', line);
    if (updates.name !== undefined) fd.append('name', updates.name);
    if (updates.ip !== undefined) fd.append('ip', updates.ip);
    if (updates.yx_ip !== undefined) fd.append('yx_ip', updates.yx_ip);
    try {
      await api('/config', { method: 'POST', body: fd });
      closeDetailDrawer();
      ttoast('t_updated');
      loadData();
    } catch (err) {
      ttoast('t_update_fail', apiError(err));
    }
  };
  // 设为优选按钮
  document.getElementById('drawerSelect').onclick = async () => {
    // 读取抽屉里编辑后的值
    const ipInput = content.querySelector('[data-key="ip"]');
    const yxInput = content.querySelector('[data-key="yx_ip"]');
    const nameInput = content.querySelector('[data-key="name"]');
    const ipVal = ipInput ? ipInput.value.trim() : item.ip;
    const yxVal = yxInput ? yxInput.value.trim() : item.yx_ip;
    const nameVal = nameInput ? nameInput.value.trim() : item.name;
    const ipToUse = yxVal || ipVal;
    if (!ipToUse || ipToUse === 'DIRECT') {
      ttoast('t_no_ip_node');
      return;
    }
    let pre = nameVal || 'default';
    if (pre.includes('-')) pre = pre.split('-')[0].trim();
    pre = pre + ' - ';
    try {
      await api(`/api/preip?type=set&pre=${encodeURIComponent(pre)}&ip=${encodeURIComponent(ipToUse)}`);
      closeDetailDrawer();
      ttoast('t_set_pre_ok', ipToUse);
    } catch (err) {
      ttoast('t_set_pre_fail', apiError(err));
    }
  };
  document.getElementById('drawerScrim').classList.remove('hidden');
  document.getElementById('detailDrawer').classList.remove('hidden');
  document.body.style.overflow = 'hidden'; // 阻止背景滚动
}
function closeDetailDrawer() {
  document.getElementById('drawerScrim').classList.add('hidden');
  document.getElementById('detailDrawer').classList.add('hidden');
  document.body.style.overflow = ''; // 恢复滚动
}

function bindButtons() {
  document.getElementById('refreshBtn').addEventListener('click', loadData);
  document.getElementById('addBtn').addEventListener('click', addInlineRow);
  document.getElementById('fabAdd').addEventListener('click', addInlineRow);
  document.getElementById('batchBtn').addEventListener('click', () => openBatchDialog());
  document.getElementById('sortBtn').addEventListener('click', toggleSort);
  document.getElementById('importBtn').addEventListener('click', openImportDialog);
  document.getElementById('exportBtn').addEventListener('click', exportNodes);
  document.getElementById('selectBtn').addEventListener('click', toggleSelect);
  // 抽屉关闭
  document.getElementById('drawerClose').addEventListener('click', closeDetailDrawer);
  document.getElementById('drawerScrim').addEventListener('click', closeDetailDrawer);
  // 选择模式操作栏
  const selectBar = document.getElementById('selectBar');
  if (selectBar) {
    selectBar.querySelector('#selectAllBtn').addEventListener('click', selectAll);
    selectBar.querySelector('#deleteSelectedBtn').addEventListener('click', deleteSelected);
    selectBar.querySelector('#batchEditBtn').addEventListener('click', openBatchEditDialog);
    selectBar.querySelector('#exitSelectBtn').addEventListener('click', () => toggleSelect());
  }
  // 搜索
  const searchInput = document.getElementById('searchInput');
  const searchClear = document.getElementById('searchClear');
  searchInput.addEventListener('input', (e) => {
    searchQuery = e.target.value.trim();
    searchClear.classList.toggle('hidden', !searchQuery);
    renderTable();
  });
  searchClear.addEventListener('click', () => {
    searchInput.value = '';
    searchQuery = '';
    searchClear.classList.add('hidden');
    renderTable();
    searchInput.focus();
  });
}

// 键盘快捷键
function bindShortcuts() {
  document.addEventListener('keydown', (e) => {
    // 忽略输入框内的按键
    const tag = (e.target.tagName || '').toLowerCase();
    const inField = tag === 'input' || tag === 'textarea' || tag === 'select';
    // Esc 任何情况都处理
    if (e.key === 'Escape') {
      if (isSortMode) { exitSort(); return; }
      if (isSelectMode) { exitSelect(); return; }
      return;
    }
    if (inField) return;
    // / 聚焦搜索
    if (e.key === '/') {
      e.preventDefault();
      document.getElementById('searchInput').focus();
      return;
    }
    // n 新建
    if (e.key === 'n' || e.key === 'N') {
      if (isSortMode || isSelectMode) return;
      e.preventDefault();
      addInlineRow();
      return;
    }
    // Delete 删除选中（选择模式下）
    if ((e.key === 'Delete' || e.key === 'Backspace') && isSelectMode) {
      e.preventDefault();
      deleteSelected();
      return;
    }
    // s 排序模式
    if (e.key === 's' || e.key === 'S') {
      if (isSelectMode) return;
      e.preventDefault();
      toggleSort();
      return;
    }
    // ? 帮助由全局 m3e.js 统一处理（showGlobalHelp 检测 helpDialog 存在直接用）
  });
}

function setupHelpDialog() {
  const d = document.getElementById('helpDialog');
  const s = document.getElementById('helpScrim');
  window._helpCtrl = dialog(d, s);
  document.getElementById('helpOk').addEventListener('click', () => window._helpCtrl.close());
}
function openHelpDialog() {
  if (window._helpCtrl) window._helpCtrl.open();
}

// 内联新增行序号：避免固定 id 在多行同时新增时冲突
let newRowSeq = 0;
function addInlineRow() {
  if (isSortMode) return ttoast('t_exit_sort');
  const seq = ++newRowSeq;
  const tbody = document.getElementById('tableBody');
  const tr = document.createElement('tr');
  tr.className = 'new-row';
  tr.innerHTML = `
    <td data-label="${i18n.t('row_num')}" class="row-num">N</td>
    <td data-label="${i18n.t('proxy_ip')}"><input class="edit-input" id="newIp-${seq}" placeholder="${i18n.t('new_ip_ph')}"></td>
    <td data-label="${i18n.t('yx_ip')}"><input class="edit-input" id="newYx-${seq}" placeholder="${i18n.t('new_yx_ph')}"></td>
    <td data-label="${i18n.t('name')}"><input class="edit-input" id="newName-${seq}" placeholder="${i18n.t('new_name_ph')}" required></td>
    <td data-label="${i18n.t('actions')}" class="action-cell">
      <button class="action-btn save" id="newSave-${seq}" title="${i18n.t('save')}"><span class="material-symbols-rounded">check</span></button>
      <button class="action-btn cancel" id="newCancel-${seq}" title="${i18n.t('cancel')}"><span class="material-symbols-rounded">close</span></button>
    </td>
  `;
  tbody.insertBefore(tr, tbody.firstChild);
  tr.querySelector(`#newSave-${seq}`).addEventListener('click', () => saveNew(seq));
  tr.querySelector(`#newCancel-${seq}`).addEventListener('click', () => tr.remove());
  tr.querySelector(`#newName-${seq}`).focus();
}

async function saveNew(seq) {
  const ip = document.getElementById(`newIp-${seq}`).value.trim();
  const name = document.getElementById(`newName-${seq}`).value.trim();
  const yx = document.getElementById(`newYx-${seq}`).value.trim();
  if (!name) return ttoast('t_name_empty');
  const fd = new FormData();
  fd.append('type', 'add');
  fd.append('ip', ip);
  fd.append('name', name);
  if (yx) fd.append('yx_ip', yx);
  isEditing = true;
  try {
    await api('/config', { method: 'POST', body: fd });
    ttoast('t_added');
    loadData();
  } catch (err) {
    ttoast('t_add_fail', apiError(err));
  } finally {
    isEditing = false;
  }
}

function editRow(line) {
  if (isSortMode) return;
  const tr = document.querySelector(`tr[data-line="${line}"]`);
  if (!tr) return;
  const item = data[line - 1];
  tr.innerHTML = `
    <td data-label="行号" class="row-num">${line}</td>
    <td data-label="ProxyIP"><input class="edit-input" id="edIp-${line}" value="${escapeHtml(item.ip || '')}"></td>
    <td data-label="优选 IP"><input class="edit-input" id="edYx-${line}" value="${escapeHtml(item.yx_ip || '')}"></td>
    <td data-label="名称"><input class="edit-input" id="edName-${line}" value="${escapeHtml(item.name)}"></td>
    <td data-label="操作" class="action-cell">
      <button class="action-btn save" id="edSave-${line}" title="保存"><span class="material-symbols-rounded">check</span></button>
      <button class="action-btn cancel" id="edCancel-${line}" title="取消"><span class="material-symbols-rounded">close</span></button>
    </td>
  `;
  tr.querySelector(`#edSave-${line}`).addEventListener('click', () => saveEdit(line));
  tr.querySelector(`#edCancel-${line}`).addEventListener('click', renderTable);
}

async function saveEdit(line) {
  const ip = document.getElementById(`edIp-${line}`).value.trim();
  const yx = document.getElementById(`edYx-${line}`).value.trim();
  const name = document.getElementById(`edName-${line}`).value.trim();
  if (!name) return ttoast('t_name_empty');
  const orig = data[line - 1];
  const fd = new FormData();
  fd.append('type', 'mod');
  fd.append('line', line);
  if (ip !== (orig.ip || '')) fd.append('ip', ip);
  if (yx !== (orig.yx_ip || '')) fd.append('yx_ip', yx);
  if (name !== orig.name) fd.append('name', name);
  isEditing = true;
  try {
    await api('/config', { method: 'POST', body: fd });
    ttoast('t_updated');
    loadData();
  } catch (err) {
    ttoast('t_update_fail', apiError(err));
  } finally {
    isEditing = false;
  }
}

function toggleSort() {
  isSortMode ? exitSort() : enterSort();
}
function enterSort() {
  isSortMode = true;
  selectedRow = null;
  const btn = document.getElementById('sortBtn');
  btn.innerHTML = '<span class="material-symbols-rounded">close</span>' + i18n.t('exit_sort');
  btn.classList.remove('btn-outline');
  btn.classList.add('btn-danger');
  document.getElementById('fabAdd').classList.add('hidden'); // 隐藏 FAB
  ttoast('t_sort_hint');
  renderTable();
}
function exitSort() {
  isSortMode = false;
  selectedRow = null;
  const btn = document.getElementById('sortBtn');
  btn.innerHTML = '<span class="material-symbols-rounded">swap_vert</span>' + i18n.t('sort');
  btn.classList.remove('btn-danger');
  btn.classList.add('btn-outline');
  document.getElementById('fabAdd').classList.remove('hidden'); // 恢复 FAB
  renderTable();
}

// 选择模式（批量删除）
function toggleSelect() {
  if (isSortMode) return ttoast('t_exit_sort');
  isSelectMode ? exitSelect() : enterSelect();
}
function enterSelect() {
  isSelectMode = true;
  selectedLines.clear();
  const btn = document.getElementById('selectBtn');
  btn.innerHTML = '<span class="material-symbols-rounded">close</span>' + i18n.t('exit_select');
  btn.classList.remove('btn-outline');
  btn.classList.add('btn-filled-tonal');
  document.getElementById('selectBar').classList.remove('hidden');
  document.getElementById('fabAdd').classList.add('hidden'); // 隐藏 FAB
  renderTable();
  updateSelectBar();
}
function exitSelect() {
  isSelectMode = false;
  selectedLines.clear();
  const btn = document.getElementById('selectBtn');
  btn.innerHTML = '<span class="material-symbols-rounded">checklist</span>' + i18n.t('select');
  btn.classList.remove('btn-filled-tonal');
  btn.classList.add('btn-outline');
  document.getElementById('selectBar').classList.add('hidden');
  document.getElementById('fabAdd').classList.remove('hidden'); // 恢复 FAB
  renderTable();
}
function updateSelectBar() {
  document.getElementById('selectCount').textContent = `${i18n.t('select_count')} ${selectedLines.size} ${i18n.t('select_count_suffix')}`;
}
function selectAll() {
  // 全选当前过滤后的所有行
  const allLines = data.map((_, idx) => idx + 1);
  // 如果搜索了，只选匹配的
  if (searchQuery) {
    const q = searchQuery.toLowerCase();
    selectedLines = new Set();
    data.forEach((item, idx) => {
      if ((item.name || '').toLowerCase().includes(q)
        || (item.ip || '').toLowerCase().includes(q)
        || (item.yx_ip || '').toLowerCase().includes(q)) {
        selectedLines.add(idx + 1);
      }
    });
  } else {
    selectedLines = new Set(allLines);
  }
  renderTable();
  updateSelectBar();
}
async function deleteSelected() {
  if (selectedLines.size === 0) return ttoast('t_no_select');
  const ok = await confirmDialog(i18n.t('cd_batch_del_title'), i18n.t('cd_batch_del_content').replace('{n}', selectedLines.size));
  if (!ok) return;
  const lines = Array.from(selectedLines);
  isEditing = true;
  try {
    const result = await api('/api/batch_delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ lines }),
    });
    toast(i18n.t('t_batch_del_ok_tpl').replace('{s}', result.success) + (result.failed > 0 ? i18n.t('t_with_fail').replace('{n}', result.failed) : ''));
  } catch (err) {
    ttoast('t_batch_del_fail', apiError(err));
  }
  isEditing = false;
  exitSelect();
  loadData();
}

// 批量编辑
function setupBatchEditDialog() {
  const d = document.getElementById('batchEditDialog');
  const s = document.getElementById('batchEditScrim');
  window._batchEditCtrl = dialog(d, s);
  document.getElementById('batchEditCancel').addEventListener('click', () => window._batchEditCtrl.close());
  document.getElementById('batchEditOk').addEventListener('click', doBatchEdit);
}
function openBatchEditDialog() {
  if (selectedLines.size === 0) return ttoast('t_select_first');
  document.getElementById('batchEditName').value = '';
  document.getElementById('batchEditIp').value = '';
  document.getElementById('batchEditYxIp').value = '';
  window._batchEditCtrl.open();
}
async function doBatchEdit() {
  const name = document.getElementById('batchEditName').value.trim();
  const ip = document.getElementById('batchEditIp').value.trim();
  const yxIp = document.getElementById('batchEditYxIp').value.trim();
  if (!name && !ip && !yxIp) return ttoast('t_fill_one');
  const lines = Array.from(selectedLines).sort((a, b) => b - a); // 降序避免行号变化
  let ok2 = 0, fail2 = 0;
  isEditing = true;
  for (const line of lines) {
    const fd = new FormData();
    fd.append('type', 'mod');
    fd.append('line', line);
    if (name) fd.append('name', name);
    if (ip) fd.append('ip', ip);
    if (yxIp) fd.append('yx_ip', yxIp);
    try { await api('/config', { method: 'POST', body: fd }); ok2++; }
    catch { fail2++; }
  }
  isEditing = false;
  window._batchEditCtrl.close();
  toast(i18n.t('t_batch_edit_ok_tpl').replace('{s}', ok2) + (fail2 > 0 ? i18n.t('t_with_fail').replace('{n}', fail2) : ''));
  exitSelect();
  loadData();
}
function selectForSort(tr) {
  if (!isSortMode) return;
  if (!selectedRow) {
    selectedRow = tr;
    tr.classList.add('selected-row');
  } else if (selectedRow === tr) {
    tr.classList.remove('selected-row');
    selectedRow = null;
  } else {
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
  } catch (err) {
    ttoast('t_swap_fail', apiError(err));
    exitSort();
  }
}

// 插入式移动：把 fromLine 移动到 toLine 位置
async function moveToLine(fromLine, toLine) {
  if (fromLine === toLine) return;
  const fd = new FormData();
  fd.append('type', 'move_to');
  fd.append('from', fromLine);
  fd.append('to', toLine);
  try {
    await api('/config', { method: 'POST', body: fd });
    ttoast('t_move_ok');
    loadData();
  } catch (err) {
    ttoast('t_move_fail', apiError(err));
    exitSort();
  }
}

// 批量添加
function setupBatchDialog() {
  const d = document.getElementById('batchDialog');
  const s = document.getElementById('batchScrim');
  const ctrl = dialog(d, s);
  window._batchCtrl = ctrl;
  document.getElementById('batchCancel').addEventListener('click', ctrl.close);
  document.getElementById('batchOk').addEventListener('click', batchAdd);
}
function openBatchDialog() {
  document.getElementById('batchName').value = '';
  document.getElementById('batchIps').value = '';
  document.getElementById('batchYxIp').value = '';
  window._batchCtrl.open();
}
async function batchAdd() {
  const name = document.getElementById('batchName').value.trim();
  const ipsText = document.getElementById('batchIps').value;
  const yx = document.getElementById('batchYxIp').value.trim();
  if (!name) return ttoast('t_name_empty');
  const ips = ipsText.split('\n').map(s => s.trim()).filter(Boolean);
  if (ips.length === 0) return ttoast('t_enter_ip');
  let ok = 0, fail = 0;
  isEditing = true;
  for (const ip of ips) {
    const fd = new FormData();
    fd.append('type', 'add');
    fd.append('ip', ip);
    fd.append('name', name);
    if (yx) fd.append('yx_ip', yx);
    try { await api('/config', { method: 'POST', body: fd }); ok++; }
    catch { fail++; }
  }
  isEditing = false;
  window._batchCtrl.close();
  toast(i18n.t('t_batch_add_ok_tpl').replace('{s}', ok).replace('{f}', fail));
  loadData();
}

// 导入节点（vless:// 批量）
function setupImportDialog() {
  const d = document.getElementById('importDialog');
  const s = document.getElementById('importScrim');
  const ctrl = dialog(d, s);
  window._importCtrl = ctrl;
  document.getElementById('importCancel').addEventListener('click', ctrl.close);
  document.getElementById('importOk').addEventListener('click', doImport);
}
function openImportDialog() {
  if (isSortMode) return ttoast('t_exit_sort');
  document.getElementById('importText').value = '';
  window._importCtrl.open();
}
async function doImport() {
  const text = document.getElementById('importText').value.trim();
  if (!text) return ttoast('t_paste_vless');
  const mode = document.getElementById('importMode').value;
  const ctrl = window._importCtrl;
  const btn = document.getElementById('importOk');
  try {
    isEditing = true;
    btn.disabled = true;
    btn.textContent = i18n.t('importing');
    const result = await api('/api/import?mode=' + encodeURIComponent(mode), {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: text,
    });
    btn.disabled = false;
    btn.textContent = i18n.t('import');
    ctrl.close();
    // 拼接结果消息，含重复信息（模板均走词条，避免中英混杂）
    let msg = i18n.t('t_import_ok_tpl').replace('{s}', result.success);
    if (result.skipped > 0) msg += i18n.t('t_import_skip_tpl').replace('{n}', result.skipped);
    if (result.duplicates > 0 && result.skipped === 0) msg += i18n.t('t_import_dup_tpl').replace('{n}', result.duplicates);
    if (result.failed > 0) msg += i18n.t('t_import_fail_part_tpl').replace('{n}', result.failed);
    toast(msg);
    loadData();
  } catch (err) {
    isEditing = false;
    btn.disabled = false;
    btn.textContent = i18n.t('import');
    ttoast('t_import_fail', apiError(err));
  } finally {
    isEditing = false;
  }
}

// 导出节点（下载 vless.txt 原文）
function exportNodes() {
  window.location.href = url('/api/export');
}

