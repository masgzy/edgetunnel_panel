// M3E 通用工具：主题切换、toast/snackbar、ripple、dialog、fetch 封装。
// ES module，导出供页面使用。
// 混合式接入：交互重的 dialog/snackbar/switch 由 m3e Web Components 渲染（见 assets/vendor/m3e.bundle.js）。
import '/assets/vendor/m3e.bundle.js';

/** 主题管理 */
export const theme = {
  KEY: 'edt-theme',
  get() {
    return localStorage.getItem(this.KEY) || 'system';
  },
  set(mode) { // 'light' | 'dark' | 'system'
    localStorage.setItem(this.KEY, mode);
    this.apply();
  },
  apply() {
    const mode = this.get();
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const isDark = mode === 'dark' || (mode === 'system' && prefersDark);
    document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light');
    // 更新主题切换 icon（三态：light_mode / dark_mode / brightness_auto）
    const icon = document.querySelector('[data-theme-icon]');
    if (icon) {
      if (mode === 'system') icon.textContent = 'brightness_auto';
      else icon.textContent = isDark ? 'light_mode' : 'dark_mode';
    }
    // 同步 settings 页 theme chip 高亮（若存在）
    document.querySelectorAll('.theme-chip').forEach(chip => {
      chip.classList.toggle('selected', chip.dataset.themeMode === mode);
    });
  },
  toggle() {
    const cur = this.get();
    // 三态循环：light → dark → system → light
    const next = cur === 'light' ? 'dark' : cur === 'dark' ? 'system' : 'light';
    this.set(next);
  }
};

// 跟随系统变化
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
  if (theme.get() === 'system') theme.apply();
});

/** i18n 国际化（中/英） */
const I18N_KEY = 'edt-lang';
const translations = {
  'zh': {
    'dashboard': '仪表盘', 'nodes': '节点配置', 'selector': '优选 IP', 'settings': '设置',
    'ok': '知道了', 'cancel': '取消', 'confirm': '确定',
    'app_title': 'edt_panel 订阅管理', 'refresh': '刷新', 'add': '添加', 'batch': '批量',
    'import': '导入', 'export': '导出', 'sort': '排序', 'select': '选择',
    'search_placeholder': '搜索名称 / IP / 优选 IP…',
    'nodes_title': '节点配置', 'nodes_subtitle': '管理 vless.txt 节点列表 · 支持 增删改查、批量添加、拖拽排序',
    'row_num': '行号', 'proxy_ip': 'ProxyIP', 'yx_ip': '优选 IP', 'name': '名称', 'actions': '操作',
    'edit': '编辑', 'delete': '删除', 'save': '保存修改', 'set_preferred': '设为优选',
    'detail_title': '节点详情', 'select_all': '全选', 'delete_selected': '删除选中',
    'batch_edit': '批量编辑', 'exit': '退出', 'selected': '已选', 'items': '项',
    'help_title': '键盘快捷键', 'ok': '知道了',
    'dashboard_title': '订阅管理仪表盘', 'dashboard_subtitle': '查看节点状态、获取订阅信息、生成订阅链接',
    'service_status': '服务状态', 'sub_port': '订阅端口', 'default_profile': '默认模板',
    'b64_encode': 'Base64 编码', 'call_stats': '调用统计', 'sub_gen': '订阅生成', 'mihomo_config': 'mihomo 配置',
    'format_convert': '格式转换', 'ip_query': 'IP 查询', 'uuid_query': 'UUID 查询', 'uptime': '运行时长',
    'sub_generator': '订阅生成器', 'sub_link': '订阅链接', 'copy_link': '复制链接',
    'open_new': '新窗口打开', 'download': '下载', 'qr_code': '二维码', 'scan_to_import': '扫码导入订阅',
    'sub_history': '订阅历史', 'history_subtitle': '最近生成的链接（本地保存）', 'clear_history': '清空历史',
    'no_history': '暂无历史',
    // selector 页
    'selector_title': '优选 IP', 'selector_subtitle': '选择当前生效的优选 IP 与前缀 · 写入 pre_ip.txt',
    'current_effective': '当前生效', 'prefix': '前缀', 'ip_addr': 'IP',
    'customize': '自定义', 'custom_title': '自定义优选 IP', 'determine': '确定',
    'set_current': '设为当前',
    // settings 页
    'appearance': '外观', 'theme_mode': '主题模式', 'light': '浅色', 'dark': '深色', 'follow_system': '跟随系统',
    'gen_title': '生成配置', 'gen_subtitle': '订阅链接的协议与连接参数',
    'gen_auto_label': '自动获取配置(协议，设置)',
    'gen_auto_hint': '勾选时优先从面板 config.json 读取；面板不可用时回落手动默认值。取消勾选则使用下方手动设置。',
    'gen_panel_source': '配置来源：面板',
    'gen_panel_hosts': '域名池',
    'gen_agg_label': '聚合面板原生订阅',
    'gen_agg_hint': '开启后订阅输出合并面板(Worker) /sub 原生节点（含优选 IP 池与反代注入），保存并重启生效。',
    'gen_panel_unavailable': '暂无面板快照：首次请求订阅或点击刷新后重试；若持续不可用请改用手动设置。',
    'gen_protocol': '协议', 'gen_transport': '传输', 'gen_fingerprint': '浏览器指纹', 'gen_fragment': 'TLS 分片',
    'gen_ss': 'Shadowsocks',
    'gen_frag_off': '关闭',
    'gen_grpc_ua': 'gRPC User-Agent',
    'gen_skip_cert': '跳过证书验证', 'gen_0rtt': '启用 0-RTT', 'gen_random_path': '随机伪装路径',
    'gen_ech_dns': 'ECH Config DNS', 'gen_ech_sni': 'ECH SNI（可选）',
    'gen_save_hint': '保存后需重启服务生效；手动模式取值仅作为面板不可用时的回落默认。',
    'sc_bridge': 'subconverter 桥接', 'sc_subtitle': '异构格式转换后端', 'mode': '模式',
    'remote_addr': '远程地址', 'local_port': '本地端口',
    'config_file': '配置文件', 'save_btn': '保存', 'config_subtitle': 'config.yml', 'config_save_hint': '保存后需重启服务生效', 'copy_all': '复制全部',
    'data_backup': '数据备份与恢复', 'backup_subtitle': '导出 / 导入 data 目录',
    'backup_desc': '备份包含 vless.txt、result.csv、pre_ip.txt、stats.json 等数据文件。恢复会覆盖现有 data 目录。',
    'download_backup': '下载备份', 'restore_backup': '恢复备份',
    'run_log': '运行日志', 'log_subtitle': '最近 200 行',
    'about': '关于', 'about_subtitle': 'edt_panel 订阅管理服务', 'version': '版本', 'runtime': '运行时',
    'ui': 'UI', 'source': '源码',
    'uuid_card': 'UUID', 'uuid_subtitle': '动态/静态订阅标识', 'get': '获取', 'copy': '复制', 'clear': '清除',
    'ip_card': '首选测速 IP', 'ip_card_subtitle': 'result.csv 首行', 'get_ip': '获取', 'copy_ip': '复制 IP',
    'domain_card': '订阅域名', 'domain_subtitle': '当前模板 SNI', 'get_domain': '获取',
    'quick_links': '快捷入口', 'quick_subtitle': '常用页面',
    'data_source': '数据源', 'sub_templates': '订阅模板',
    // placeholder 补充
    'custom_pre_ph': '例如：日本 - ', 'custom_ip_ph': '1.2.3.4 或域名',
    'batch_name_ph': '例如：日本节点', 'batch_ips_ph': '192.168.1.1\n192.168.1.2\n192.168.1.3',
    'import_ph': 'vless://uuid@host:port/?type=ws&security=tls&path=...&host=...&sni=...#名称\nvless://...',
    'new_name_ph': '名称', 'new_ip_ph': 'ProxyIP', 'new_yx_ph': '优选 IP',
    'batch_edit_name_ph': '留空保持原值', 'batch_edit_ip_ph': '留空保持原值', 'batch_edit_yx_ph': '留空保持原值',
    // 仪表盘信息卡片
    'uuid_card_title': 'UUID', 'ip_card_title': '首选测速 IP', 'domain_card_title': '订阅域名',
    'uuid_input_ph': '点击下方按钮获取', 'domain_input_ph': '点击下方按钮获取',
    'get_uuid': '获取 UUID', 'get_ip_btn': '获取 IP', 'get_domain_btn': '获取域名',
    'clear_cache': '清除缓存', 'copy_uuid': '复制', 'copy_ip_btn': '复制 IP', 'copy_domain_btn': '复制',
    'no_data': '点击下方按钮获取', 'ip_info': 'IP 信息',
    'stat_item_sub': '订阅生成 /sub', 'stat_item_mihomo': 'mihomo 配置',
    'stat_item_convert': '格式转换', 'stat_item_getip': 'IP 查询', 'stat_item_getuuid': 'UUID 查询',
    // 表头
    'th_row_num': '行号', 'th_proxy_ip': 'ProxyIP', 'th_yx_ip': '优选 IP', 'th_name': '名称', 'th_actions': '操作', 'th_select': '选择',
    // settings 关于 kv
    'kv_version': '版本', 'kv_runtime': '运行时', 'kv_ui': 'UI', 'kv_source': '源码',
    'kv_value_runtime': 'Go 单二进制', 'kv_value_ui': 'Material Design 3 Expressive', 'kv_value_source': 'edt (Go 1.21+)',
    'about_desc': '由原 Python Flask 项目重写为纯 Go 实现，无外部运行时依赖。',
    // toast 消息
    't_copied': '已复制到剪贴板', 't_copy_fail': '复制失败，请手动选择文本',
    't_uuid_ok': 'UUID 获取成功', 't_uuid_fail': '获取 UUID 失败：',
    't_uuid_cleared': 'UUID 缓存已清除', 't_clear_fail': '清除失败：',
    't_ip_ok': 'IP 信息获取成功', 't_ip_fail': '获取 IP 失败：',
    't_domain_ok': '域名获取成功', 't_domain_fail': '获取域名失败：',
    't_no_uuid': '请先获取 UUID', 't_no_ip': '请先获取 IP', 't_no_domain': '请先获取域名', 't_no_param': '请先选择参数',
    't_added': '已添加', 't_add_fail': '添加失败：', 't_updated': '已更新', 't_update_fail': '更新失败：',
    't_deleted': '已删除', 't_del_fail': '删除失败：', 't_swap_ok': '已交换', 't_swap_fail': '交换顺序失败：',
    't_move_ok': '已移动', 't_move_fail': '移动失败：',
    't_sort_hint': '拖拽行排序，或点击两行交换', 't_select_hint': '点击两行交换顺序',
    't_exit_sort': '请先退出排序模式', 't_exit_select': '请先退出选择模式',
    't_name_empty': '名称不能为空', 't_no_change': '无修改', 't_no_history': '暂无历史记录',
    't_batch_ok': '批量添加完成：', 't_batch_del_ok': '批量删除完成：',
    't_import_ok': '导入完成：', 't_import_fail': '导入失败：',
    't_qr_ok': '已生成二维码', 't_share_ok': '分享链接已复制（含订阅参数）',
    't_share_restore': '已从分享链接还原订阅参数',
    't_set_pre_ok': '已设为优选：', 't_set_pre_fail': '设置失败：',
    't_no_ip_node': '此节点没有有效 IP', 't_history_cleared': '历史已清空',
    't_backup_start': '开始下载备份', 't_restore_ok': '恢复完成：', 't_restore_fail': '恢复失败：',
    't_load_fail': '加载失败：', 't_refreshed': '已刷新', 't_set_cur_ok': '已设为当前：',
    't_set_ok': '已设置', 't_pre_ip_empty': '前缀和 IP 都不能为空', 't_config_not_loaded': '配置未加载', 't_config_saved': '配置已保存，重启生效', 't_config_save_fail': '保存失败', 't_sc_installed': 'subconverter 安装成功', 't_sc_install_fail': '安装失败', 'sc_tip': 'edt_panel 原生支持 vless/mihomo，subconverter 用于 clashr/surge/quanx 等格式转换。保存后需重启生效。', 'general_settings': '通用设置', 'general_subtitle': '订阅端口 / 默认模板 / 偏好', 'history_limit': '订阅历史显示数量', 'log_lines': '实时日志显示行数',
    'sub_gen_subtitle': '按需选择参数，生成订阅链接',
    'ds_id': '数据源 ID', 'sub_type': '订阅模板 type', 'output_format': '输出格式',
    'acl4ssr_ruleset': 'Clash 规则集（ACL4SSR）', 'default': '默认',
    'share': '分享', 'qr_code': '二维码',

    't_help_title': '键盘快捷键', 't_ok': '知道了',
    't_help_show': '显示本帮助', 't_help_esc': '关闭弹窗/抽屉',
    't_help_theme_kbd': '点击主题图标', 't_help_theme': '切换 浅色/深色/跟随系统',
    't_help_lang_kbd': '点击语言图标', 't_help_lang': '切换 中/英文',
    't_help_nodes': '节点配置页有更多快捷键：/ N S Del',
    // 主题色
    'theme_color': '主题色', 'theme_color_subtitle': '从种子色实时生成整套 M3 色板（HCT）',
    'theme_color_custom': '自定义', 'theme_reset': '默认', 't_seed_ok': '主题色已更新',
    // 导航
    'nav_dashboard': '仪表盘', 'nav_nodes': '节点配置', 'nav_selector': '优选 IP', 'nav_settings': '设置',
    // IP 信息卡
    'ip_send_recv': '发包/接收', 'ip_plr': '丢包率', 'ip_ping': '延迟', 'ip_speed': '下载速度', 'ip_region': '地区',
    'cd_clear_history_title': '清空订阅历史', 'cd_clear_history_content': '将删除本地保存的全部订阅链接记录，确定？',
    'batch_add_title': '批量添加节点', 'import_title': '导入节点',
    'batch_edit_title': '批量编辑选中节点',
    'empty_no_nodes': '暂无节点，点击上方"添加"', 'empty_no_match': '无匹配节点',
    'empty_no_nodes_selector': '暂无节点', 'empty_no_template': '无模板', 'empty_no_ds': '无数据源',
    'select_count': '已选', 'select_count_suffix': '项',
    'exit_sort': '退出排序', 'enabled': '已启用', 'exit_select': '退出选择',
    't_load_fail': '加载失败：', 't_del_fail': '删除失败：',
    't_no_select': '未选择任何节点', 't_select_first': '请先选择节点',
    't_fill_one': '请至少填写一个字段', 't_batch_del_fail': '批量删除失败：',
    't_enter_ip': '请输入至少一个 IP', 't_paste_vless': '请粘贴 vless:// 链接',
    't_import_fail': '导入失败：', 't_no_ip_node': '此节点没有有效 IP',
    // 复合 toast 模板
    't_batch_del_ok_tpl': '批量删除完成：{s} 成功', 't_batch_edit_ok_tpl': '批量编辑完成：{s} 成功',
    't_batch_add_ok_tpl': '批量添加完成：{s} 成功，{f} 失败',
    't_with_fail': '，{n} 失败',
    'cd_del_title': '删除节点', 'cd_del_content': '确定删除第 {line} 行「{name}」吗？',
    'cd_batch_del_title': '批量删除', 'cd_batch_del_content': '确定删除选中的 {n} 个节点吗？此操作不可撤销。',
    'cd_restore_title': '恢复备份', 'cd_restore_content': '确定用 {name} 恢复吗？这将覆盖当前 data 目录。',
    // 导入结果补充
    't_import_skip_tpl': '，{n} 跳过', 't_import_dup_tpl': '，{n} 覆盖', 't_import_fail_part_tpl': '，{n} 失败',
    // 节点计数
    'count_nodes': '{n} 个节点',
    // 日志
    'no_logs': '（暂无日志）',
    // subconverter 模式选项
    'mode_off': 'Off（禁用）', 'mode_local': 'Local（本地二进制）', 'mode_remote': 'Remote（远程后端）',
    'pick_remote': '-- 选择远程后端 --',
    'sc_bin_label': 'subconverter 二进制', 'sc_install_btn': '下载安装', 'sc_threads': '下载线程数',
    'sc_installed': '✓ 已安装', 'sc_not_installed': '✗ 未安装', 'sc_checking': '检查中…',
    'custom_remote_ph': '或输入自定义地址', 'b64_label': 'Base64 编码',
    // 错误码翻译
    'err_INTERNAL': '内部错误', 'err_BAD_REQUEST': '请求参数错误',
    'err_NOT_FOUND': '资源不存在', 'err_METHOD_NOT_ALLOWED': '方法不支持',
    'err_BAD_GATEWAY': '网关错误', 'err_NETWORK': '网络请求失败',
    // 导入预览
    'import_preview_title': '导入预览',
    'import_preview': '解析：{n} 新增，{d} 重复（将跳过），{v} 无效。继续导入？',
    'importing': '导入中...',
    't_import_ok_tpl': '导入完成：{s} 成功',
  },
  'en': {
    'dashboard': 'Dashboard', 'nodes': 'Nodes', 'selector': 'Selector', 'settings': 'Settings',
    'ok': 'Got it', 'cancel': 'Cancel', 'confirm': 'OK',
    'app_title': 'edt_panel Manager', 'refresh': 'Refresh', 'add': 'Add', 'batch': 'Batch',
    'import': 'Import', 'export': 'Export', 'sort': 'Sort', 'select': 'Select',
    'search_placeholder': 'Search name / IP / yx_ip…',
    'nodes_title': 'Nodes', 'nodes_subtitle': 'Manage vless.txt node list · add/edit/delete/batch/drag-sort',
    'row_num': '#', 'proxy_ip': 'ProxyIP', 'yx_ip': 'Preferred IP', 'name': 'Name', 'actions': 'Actions',
    'edit': 'Edit', 'delete': 'Delete', 'save': 'Save', 'set_preferred': 'Set Preferred',
    'detail_title': 'Node Details', 'select_all': 'Select All', 'delete_selected': 'Delete Selected',
    'batch_edit': 'Batch Edit', 'exit': 'Exit', 'selected': 'selected', 'items': 'items',
    'help_title': 'Keyboard Shortcuts', 'ok': 'Got it',
    'dashboard_title': 'Subscription Dashboard', 'dashboard_subtitle': 'View node status, get subscription info, generate links',
    'service_status': 'Service Status', 'sub_port': 'Sub Port', 'default_profile': 'Default Profile',
    'b64_encode': 'Base64 Encode', 'call_stats': 'Call Stats', 'sub_gen': 'Sub Gen', 'mihomo_config': 'Mihomo Config',
    'format_convert': 'Format Convert', 'ip_query': 'IP Query', 'uuid_query': 'UUID Query', 'uptime': 'Uptime',
    'sub_generator': 'Subscription Generator', 'sub_link': 'Subscription Link', 'copy_link': 'Copy Link',
    'open_new': 'Open in New Tab', 'download': 'Download', 'qr_code': 'QR Code', 'scan_to_import': 'Scan to import',
    'sub_history': 'Subscription History', 'history_subtitle': 'Recent links (local)', 'clear_history': 'Clear History',
    'no_history': 'No history',
    // selector 页
    'selector_title': 'Preferred IP', 'selector_subtitle': 'Select current preferred IP and prefix · write to pre_ip.txt',
    'current_effective': 'Current', 'prefix': 'Prefix', 'ip_addr': 'IP',
    'customize': 'Custom', 'custom_title': 'Custom Preferred IP', 'determine': 'OK',
    'set_current': 'Set Current',
    // settings 页
    'appearance': 'Appearance', 'theme_mode': 'Theme Mode', 'light': 'Light', 'dark': 'Dark', 'follow_system': 'System',
    'gen_title': 'Generation Config', 'gen_subtitle': 'Protocol & connection parameters for subscriptions',
    'gen_auto_label': 'Auto-fetch config (protocol, settings)',
    'gen_auto_hint': 'When checked, values are read from the panel config.json first, falling back to manual defaults when unavailable. Uncheck to edit manual settings below.',
    'gen_panel_source': 'Source: panel',
    'gen_panel_hosts': 'Hosts pool',
    'gen_agg_label': 'Aggregate panel native subscription',
    'gen_agg_hint': 'Merge the panel (Worker) native /sub nodes (incl. IP pool and proxyIP injection) into the subscription; save and restart.',
    'gen_panel_unavailable': 'No panel snapshot yet: request a subscription or click refresh; use manual settings if it stays unavailable.',
    'gen_protocol': 'Protocol', 'gen_transport': 'Transport', 'gen_fingerprint': 'Fingerprint', 'gen_fragment': 'TLS Fragment',
    'gen_ss': 'Shadowsocks',
    'gen_frag_off': 'Off',
    'gen_grpc_ua': 'gRPC User-Agent',
    'gen_skip_cert': 'Skip cert verify', 'gen_0rtt': 'Enable 0-RTT', 'gen_random_path': 'Random disguise path',
    'gen_ech_dns': 'ECH Config DNS', 'gen_ech_sni': 'ECH SNI (optional)',
    'gen_save_hint': 'Restart to take effect. Manual values only apply as fallback when the panel is unavailable.',
    'sc_bridge': 'subconverter Bridge', 'sc_subtitle': 'Heterogeneous format converter', 'mode': 'Mode',
    'remote_addr': 'Remote URL', 'local_port': 'Local Port',
    'config_file': 'Config File', 'save_btn': 'Save', 'config_subtitle': 'config.yml', 'config_save_hint': 'Restart to take effect', 'copy_all': 'Copy All',
    'data_backup': 'Data Backup & Restore', 'backup_subtitle': 'Export / Import data dir',
    'backup_desc': 'Backup includes vless.txt, result.csv, pre_ip.txt, stats.json. Restore overwrites current data dir.',
    'download_backup': 'Download Backup', 'restore_backup': 'Restore Backup',
    'run_log': 'Run Log', 'log_subtitle': 'Last 200 lines',
    'about': 'About', 'about_subtitle': 'edt_panel Subscription Manager', 'version': 'Version', 'runtime': 'Runtime',
    'ui': 'UI', 'source': 'Source',
    'uuid_card': 'UUID', 'uuid_subtitle': 'Dynamic/static subscription id', 'get': 'Get', 'copy': 'Copy', 'clear': 'Clear',
    'ip_card': 'Preferred Test IP', 'ip_card_subtitle': 'result.csv first row', 'get_ip': 'Get', 'copy_ip': 'Copy IP',
    'domain_card': 'Sub Domain', 'domain_subtitle': 'Current profile SNI', 'get_domain': 'Get',
    'quick_links': 'Quick Links', 'quick_subtitle': 'Common pages',
    'data_source': 'Data Source', 'sub_templates': 'Profiles',
    // placeholder 补充
    'custom_pre_ph': 'e.g. Japan - ', 'custom_ip_ph': '1.2.3.4 or domain',
    'batch_name_ph': 'e.g. Japan nodes', 'batch_ips_ph': '192.168.1.1\n192.168.1.2\n192.168.1.3',
    'import_ph': 'vless://uuid@host:port/?type=ws&security=tls&path=...&host=...&sni=...#name\nvless://...',
    'new_name_ph': 'Name', 'new_ip_ph': 'ProxyIP', 'new_yx_ph': 'Preferred IP',
    'batch_edit_name_ph': 'leave empty to keep', 'batch_edit_ip_ph': 'leave empty to keep', 'batch_edit_yx_ph': 'leave empty to keep',
    // 仪表盘信息卡片
    'uuid_card_title': 'UUID', 'ip_card_title': 'Preferred Test IP', 'domain_card_title': 'Sub Domain',
    'uuid_input_ph': 'Click button below to get', 'domain_input_ph': 'Click button below to get',
    'get_uuid': 'Get UUID', 'get_ip_btn': 'Get IP', 'get_domain_btn': 'Get Domain',
    'clear_cache': 'Clear Cache', 'copy_uuid': 'Copy', 'copy_ip_btn': 'Copy IP', 'copy_domain_btn': 'Copy',
    'no_data': 'Click button below to get', 'ip_info': 'IP Info',
    'stat_item_sub': 'Sub Gen /sub', 'stat_item_mihomo': 'Mihomo Config',
    'stat_item_convert': 'Format Convert', 'stat_item_getip': 'IP Query', 'stat_item_getuuid': 'UUID Query',
    // 表头
    'th_row_num': '#', 'th_proxy_ip': 'ProxyIP', 'th_yx_ip': 'Preferred IP', 'th_name': 'Name', 'th_actions': 'Actions', 'th_select': 'Select',
    // settings 关于 kv
    'kv_version': 'Version', 'kv_runtime': 'Runtime', 'kv_ui': 'UI', 'kv_source': 'Source',
    'kv_value_runtime': 'Go single binary', 'kv_value_ui': 'Material Design 3 Expressive', 'kv_value_source': 'edt (Go 1.21+)',
    'about_desc': 'Rewritten from Python Flask to pure Go, no external runtime deps.',
    // toast 消息
    't_copied': 'Copied to clipboard', 't_copy_fail': 'Copy failed, select manually',
    't_uuid_ok': 'UUID fetched', 't_uuid_fail': 'Get UUID failed: ',
    't_uuid_cleared': 'UUID cache cleared', 't_clear_fail': 'Clear failed: ',
    't_ip_ok': 'IP info fetched', 't_ip_fail': 'Get IP failed: ',
    't_domain_ok': 'Domain fetched', 't_domain_fail': 'Get domain failed: ',
    't_no_uuid': 'Get UUID first', 't_no_ip': 'Get IP first', 't_no_domain': 'Get domain first', 't_no_param': 'Select params first',
    't_added': 'Added', 't_add_fail': 'Add failed: ', 't_updated': 'Updated', 't_update_fail': 'Update failed: ',
    't_deleted': 'Deleted', 't_del_fail': 'Delete failed: ', 't_swap_ok': 'Swapped', 't_swap_fail': 'Swap failed: ',
    't_move_ok': 'Moved', 't_move_fail': 'Move failed: ',
    't_sort_hint': 'Drag to sort, or click two rows to swap', 't_select_hint': 'Click two rows to swap',
    't_exit_sort': 'Exit sort mode first', 't_exit_select': 'Exit select mode first',
    't_name_empty': 'Name cannot be empty', 't_no_change': 'No changes', 't_no_history': 'No history yet',
    't_batch_ok': 'Batch add done: ', 't_batch_del_ok': 'Batch delete done: ',
    't_import_ok': 'Import done: ', 't_import_fail': 'Import failed: ',
    't_qr_ok': 'QR code generated', 't_share_ok': 'Share link copied (with params)',
    't_share_restore': 'Restored from share link',
    't_set_pre_ok': 'Set as preferred: ', 't_set_pre_fail': 'Set failed: ',
    't_no_ip_node': 'No valid IP for this node', 't_history_cleared': 'History cleared',
    't_backup_start': 'Downloading backup', 't_restore_ok': 'Restore done: ', 't_restore_fail': 'Restore failed: ',
    't_load_fail': 'Load failed: ', 't_refreshed': 'Refreshed', 't_set_cur_ok': 'Set as current: ',
    't_set_ok': 'Set', 't_pre_ip_empty': 'Prefix and IP cannot be empty', 't_config_not_loaded': 'Config not loaded', 't_config_saved': 'Saved, restart to take effect', 't_config_save_fail': 'Save failed', 't_sc_installed': 'subconverter installed', 't_sc_install_fail': 'Install failed', 'sc_tip': 'edt_panel supports vless/mihomo natively. subconverter for clashr/surge/quanx. Restart to take effect.', 'general_settings': 'General Settings', 'general_subtitle': 'Sub port / Default profile / Preferences', 'history_limit': 'History Display Count', 'log_lines': 'Log Display Lines',
    'sub_gen_subtitle': 'Select params to generate subscription link',
    'ds_id': 'Data Source ID', 'sub_type': 'Profile type', 'output_format': 'Output Format',
    'acl4ssr_ruleset': 'Clash Ruleset (ACL4SSR)', 'default': 'Default',
    'share': 'Share', 'qr_code': 'QR Code',

    't_help_title': 'Keyboard Shortcuts', 't_ok': 'Got it',
    't_help_show': 'Show this help', 't_help_esc': 'Close dialog/drawer',
    't_help_theme_kbd': 'Theme icon', 't_help_theme': 'Cycle light/dark/system',
    't_help_lang_kbd': 'Lang icon', 't_help_lang': 'Toggle zh/en',
    't_help_nodes': 'More shortcuts on Nodes page: / N S Del',
    // theme color
    'theme_color': 'Theme Color', 'theme_color_subtitle': 'Generate full M3 palette from seed (HCT)',
    'theme_color_custom': 'Custom', 'theme_reset': 'Default', 't_seed_ok': 'Theme color updated',
    // Nav
    'nav_dashboard': 'Dashboard', 'nav_nodes': 'Nodes', 'nav_selector': 'Preferred IP', 'nav_settings': 'Settings',
    // IP info card
    'ip_send_recv': 'Sent / Recv', 'ip_plr': 'Loss rate', 'ip_ping': 'Latency', 'ip_speed': 'Speed', 'ip_region': 'Region',
    'cd_clear_history_title': 'Clear subscription history', 'cd_clear_history_content': 'All locally saved subscription links will be removed. Continue?',
    'batch_add_title': 'Batch Add Nodes', 'import_title': 'Import Nodes',
    'batch_edit_title': 'Batch Edit Selected',
    'empty_no_nodes': 'No nodes. Click "Add" above', 'empty_no_match': 'No match',
    'empty_no_nodes_selector': 'No nodes', 'empty_no_template': 'No template', 'empty_no_ds': 'No data source',
    'select_count': 'selected', 'select_count_suffix': '',
    'exit_sort': 'Exit Sort', 'enabled': 'enabled', 'exit_select': 'Exit Select',
    't_load_fail': 'Load failed: ', 't_del_fail': 'Delete failed: ',
    't_no_select': 'No nodes selected', 't_select_first': 'Select nodes first',
    't_fill_one': 'Fill at least one field', 't_batch_del_fail': 'Batch delete failed: ',
    't_enter_ip': 'Enter at least one IP', 't_paste_vless': 'Paste vless:// links',
    't_import_fail': 'Import failed: ',
    // 复合 toast 模板
    't_batch_del_ok_tpl': 'Batch delete done: {s} success', 't_batch_edit_ok_tpl': 'Batch edit done: {s} success',
    't_batch_add_ok_tpl': 'Batch add done: {s} success, {f} failed',
    't_with_fail': ', {n} failed',
    'cd_del_title': 'Delete Node', 'cd_del_content': 'Delete line {line} "{name}"?',
    'cd_batch_del_title': 'Batch Delete', 'cd_batch_del_content': 'Delete {n} selected nodes? This cannot be undone.',
    'cd_restore_title': 'Restore Backup', 'cd_restore_content': 'Restore with {name}? This overwrites current data dir.',
    // Import result extra
    't_import_skip_tpl': ', {n} skipped', 't_import_dup_tpl': ', {n} overwritten', 't_import_fail_part_tpl': ', {n} failed',
    // Node count
    'count_nodes': '{n} nodes',
    // Logs
    'no_logs': '(No logs yet)',
    // Subconverter mode options
    'mode_off': 'Off (disabled)', 'mode_local': 'Local (binary)', 'mode_remote': 'Remote (backend)',
    'pick_remote': '-- Pick a remote backend --',
    'sc_bin_label': 'subconverter Binary', 'sc_install_btn': 'Install', 'sc_threads': 'Download threads',
    'sc_installed': '✓ Installed', 'sc_not_installed': '✗ Not installed', 'sc_checking': 'Checking…',
    'custom_remote_ph': 'or enter a custom URL', 'b64_label': 'Base64 Encoding',
    // 错误码翻译
    'err_INTERNAL': 'Internal error', 'err_BAD_REQUEST': 'Bad request',
    'err_NOT_FOUND': 'Not found', 'err_METHOD_NOT_ALLOWED': 'Method not allowed',
    'err_BAD_GATEWAY': 'Gateway error', 'err_NETWORK': 'Network request failed',
    // 导入预览
    'import_preview_title': 'Import Preview',
    'import_preview': 'Parsed: {n} new, {d} duplicate (will skip), {v} invalid. Continue?',
    'importing': 'Importing...',
    't_import_ok_tpl': 'Import done: {s} success',
  },
};
export const i18n = {
  get() {
    return localStorage.getItem(I18N_KEY) || 'zh';
  },
  set(lang) {
    localStorage.setItem(I18N_KEY, lang);
    this.apply();
  },
  toggle() {
    this.set(this.get() === 'zh' ? 'en' : 'zh');
  },
  t(key) {
    const lang = this.get();
    return (translations[lang] && translations[lang][key]) || key;
  },
  apply() {
    document.documentElement.lang = this.get() === 'zh' ? 'zh-CN' : 'en';
    document.querySelectorAll('[data-i18n]').forEach(el => {
      const key = el.getAttribute('data-i18n');
      const val = this.t(key);
      if (el.hasAttribute('data-i18n-placeholder')) {
        el.placeholder = val;
      } else {
        el.textContent = val;
      }
    });
    // 更新语言切换 icon
    const icon = document.querySelector('[data-lang-icon]');
    if (icon) icon.textContent = this.get() === 'zh' ? 'language' : 'translate';
  },
};

/** Ripple 效果：在元素点击点扩散圆形涟漪 */
export function attachRipple(el) {
  el.addEventListener('click', (e) => {
    const rect = el.getBoundingClientRect();
    const size = Math.max(rect.width, rect.height);
    const x = e.clientX - rect.left - size / 2;
    const y = e.clientY - rect.top - size / 2;
    const ripple = document.createElement('span');
    ripple.className = 'ripple';
    ripple.style.width = ripple.style.height = size + 'px';
    ripple.style.left = x + 'px';
    ripple.style.top = y + 'px';
    el.appendChild(ripple);
    setTimeout(() => ripple.remove(), 600);
  });
}

// 自动给所有 .btn / .icon-btn / .chip / .fab 绑定 ripple
export function autoRipple(root = document) {
  root.querySelectorAll('.btn, .icon-btn, .chip, .fab, .nav-bar a').forEach(attachRipple);
}

/** Snackbar：由 <m3e-snackbar> 渲染（popover 动效 + aria-live 内建） */
let snackbarEl = null;
function ensureSnackbar() {
  if (snackbarEl && snackbarEl.isConnected) return snackbarEl;
  snackbarEl = document.createElement('m3e-snackbar');
  snackbarEl.duration = 4000;
  snackbarEl.addEventListener('click', (e) => {
    // action 按钮点击（内部渲染的 m3e-button）
    if (snackbarEl.action && e.target.closest?.('m3e-button')) {
      const cb = snackbarEl.__onAction;
      snackbarEl.open = false;
      if (cb) cb();
    }
  });
  document.body.appendChild(snackbarEl);
  return snackbarEl;
}
export function toast(message, actionLabel, onAction) {
  const sb = ensureSnackbar();
  sb.textContent = '';
  sb.appendChild(document.createTextNode(message));
  sb.action = actionLabel || '';
  sb.__onAction = onAction || null;
  sb.open = true;
}

/** ttoast：翻译版 toast，key 是 i18n 词条 key，suffix 是附加文本（不翻译） */
export function ttoast(key, suffix = '') {
  toast(i18n.t(key) + suffix);
}

/** apiError：根据 Error 对象的 code 返回 i18n 错误消息。
 *  err 是 api() 抛出的 Error，有 .code 属性。
 *  返回翻译后的错误消息（不含后端原始详情）。
 */
export function apiError(err) {
  if (err && err.code) {
    return i18n.t('err_' + err.code);
  }
  if (err && err.message && err.message.includes('Failed to fetch')) {
    return i18n.t('err_NETWORK');
  }
  return err ? err.message : 'error';
}

function hideSnackbar() {
  if (snackbarEl) snackbarEl.open = false;
}

/** Dialog：返回 { open, close } 控制器 */
export function dialog(dialogEl, scrimEl) {
  const open = () => {
    scrimEl.classList.add('open');
    dialogEl.classList.add('open');
  };
  const close = () => {
    scrimEl.classList.remove('open');
    dialogEl.classList.remove('open');
  };
  // 点击 scrim 关闭；ESC 关闭由全局快捷键监听统一处理
  // （原先在这里给 document 挂每实例 keydown，重复创建对话框会泄漏监听器）
  if (scrimEl) {
    scrimEl.addEventListener('click', close);
  }
  return { open, close };
}

/** 确认对话框（m3e-dialog 渲染，返回 Promise<boolean>） */
export function confirmDialog(title, content) {
  return new Promise((resolve) => {
    const dlg = document.createElement('m3e-dialog');
    dlg.innerHTML = `
      <h2 slot="header">${escapeHtml(title)}</h2>
      <p>${escapeHtml(content)}</p>
      <div slot="actions" end style="display:flex;gap:8px;justify-content:flex-end;">
        <m3e-button variant="text" data-act="cancel">${i18n.t('cancel')}</m3e-button>
        <m3e-button variant="filled" data-act="ok">${i18n.t('confirm')}</m3e-button>
      </div>`;
    document.body.appendChild(dlg);
    let settled = false;
    const done = (v) => {
      if (settled) return;
      settled = true;
      resolve(v);
      try { dlg.close(); } catch (e) {}
      setTimeout(() => dlg.remove(), 350);
    };
    dlg.querySelector('[data-act="cancel"]').addEventListener('click', () => done(false));
    dlg.querySelector('[data-act="ok"]').addEventListener('click', () => done(true));
    dlg.addEventListener('close', () => done(false)); // ESC / 原生关闭
    // 延迟到当前 click 派发结束后再打开：若同步 show()，用户本次点击
    // 继续冒泡到 document 会触发 m3e-dialog 的 scrim 轻关闭，对话框一闪即逝
    setTimeout(() => dlg.show());
  });
}

/** 网关端口（历史接口；网关重写已移除，恒返回空串） */
export function gatewayPort() {
  return '';
}

/** 把路径转换成最终 URL（历史接口；现在直接返回原路径） */
export function url(path) {
  return path;
}

/** 生成完整 URL（用于需要可分享的链接，如订阅 URL）。 */
export function absoluteUrl(path) {
  return new URL(path, location.origin).href;
}

/** fetch 封装：自动处理错误、返回合适类型 */
export async function api(u, options = {}) {
  const finalUrl = url(u);
  const opts = { ...options };
  if (opts.method === 'POST' && !(opts.body instanceof FormData) && !opts.headers?.['Content-Type']) {
    opts.headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
  }
  const resp = await fetch(finalUrl, opts);
  const ct = resp.headers.get('content-type') || '';
  let data;
  if (ct.includes('application/json')) {
    data = await resp.json();
  } else {
    data = await resp.text();
  }
  if (!resp.ok) {
    const msg = typeof data === 'string' ? data : (data?.error || `HTTP ${resp.status}`);
    const code = typeof data === 'object' ? data?.code : '';
    const err = new Error(msg);
    err.code = code; // 附加错误码
    throw err;
  }
  return data;
}

/** 复制到剪贴板 */
export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(i18n.t('t_copied'));
  } catch {
    // fallback
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); toast(i18n.t('t_copied')); }
    catch { toast(i18n.t('t_copy_fail')); }
    ta.remove();
  }
}

/** HTML 转义 */
export function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}

/** 格式化数字 */
export function fmt(n, digits = 0) {
  if (n == null || isNaN(n)) return '-';
  return Number(n).toFixed(digits);
}

/** 页面初始化通用 */
export function initPage() {
  theme.apply();
  i18n.apply();
  autoRipple();
  bindGlobalShortcuts();
  registerServiceWorker();
  // 主题切换按钮
  const themeBtn = document.querySelector('[data-theme-toggle]');
  if (themeBtn) {
    themeBtn.addEventListener('click', () => theme.toggle());
  }
  // 语言切换按钮
  const langBtn = document.querySelector('[data-lang-toggle]');
  if (langBtn) {
    langBtn.addEventListener('click', () => i18n.toggle());
  }
}

// 注册 Service Worker（PWA 离线支持）
function registerServiceWorker() {
  if ('serviceWorker' in navigator) {
    // 延后注册，不阻塞首屏渲染
    const cb = () => navigator.serviceWorker.register(url('/sw.js')).catch(() => {});
    if ('requestIdleCallback' in window) {
      requestIdleCallback(cb, { timeout: 2000 });
    } else {
      setTimeout(cb, 1500);
    }
  }
  // PWA install prompt
  let deferredPrompt = null;
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault();
    deferredPrompt = e;
    // 在 top app bar 加安装按钮
    const bar = document.querySelector('.top-app-bar');
    if (bar && !bar.querySelector('.install-btn')) {
      const btn = document.createElement('button');
      btn.className = 'icon-btn install-btn';
      btn.title = '安装到桌面';
      btn.innerHTML = '<span class="material-symbols-rounded">install_mobile</span>';
      btn.addEventListener('click', async () => {
        if (deferredPrompt) {
          deferredPrompt.prompt();
          const { outcome } = await deferredPrompt.userChoice;
          if (outcome === 'accepted') {
            btn.remove();
          }
          deferredPrompt = null;
        }
      });
      // 插入到主题切换按钮前
      const themeBtn = bar.querySelector('[data-theme-toggle]');
      if (themeBtn) {
        bar.insertBefore(btn, themeBtn);
      } else {
        bar.appendChild(btn);
      }
      attachRipple(btn);
    }
  });
  // 已安装后隐藏按钮
  window.addEventListener('appinstalled', () => {
    const btn = document.querySelector('.install-btn');
    if (btn) btn.remove();
  });
}

// 全局键盘快捷键（所有页面共享）
function bindGlobalShortcuts() {
  // 避免重复绑定
  if (window.__edtShortcutsBound) return;
  window.__edtShortcutsBound = true;
  document.addEventListener('keydown', (e) => {
    const tag = (e.target.tagName || '').toLowerCase();
    const inField = tag === 'input' || tag === 'textarea' || tag === 'select';
    // ? 显示帮助（全局，非输入框时）
    if ((e.key === '?' || (e.shiftKey && e.key === '/')) && !inField) {
      e.preventDefault();
      showGlobalHelp();
      return;
    }
    // Esc 关闭所有 dialog/drawer
    if (e.key === 'Escape') {
      document.querySelectorAll('.dialog.open').forEach(d => d.classList.remove('open'));
      document.querySelectorAll('.dialog-scrim.open').forEach(s => s.classList.remove('open'));
      const drawer = document.getElementById('detailDrawer');
      const drawerScrim = document.getElementById('drawerScrim');
      if (drawer && !drawer.classList.contains('hidden')) {
        drawer.classList.add('hidden');
        if (drawerScrim) drawerScrim.classList.add('hidden');
      }
    }
  });
}

// 全局快捷键帮助弹窗（m3e-dialog 动态创建）
function showGlobalHelp() {
  const existing = document.getElementById('helpDialog');
  if (existing) {
    // nodes 页静态帮助对话框（手写版）
    existing.classList.add('open');
    document.getElementById('helpScrim').classList.add('open');
    return;
  }
  let dlg = document.querySelector('.global-help-dialog');
  if (!dlg) {
    dlg = document.createElement('m3e-dialog');
    dlg.className = 'global-help-dialog';
    dlg.innerHTML = `
      <h2 slot="header">${i18n.t('t_help_title')}</h2>
      <div class="shortcut-help-list">
        <div class="shortcut-help-row"><kbd>?</kbd><span>${i18n.t('t_help_show')}</span></div>
        <div class="shortcut-help-row"><kbd>Esc</kbd><span>${i18n.t('t_help_esc')}</span></div>
        <div class="shortcut-help-row"><kbd>${i18n.t('t_help_theme_kbd')}</kbd><span>${i18n.t('t_help_theme')}</span></div>
        <div class="shortcut-help-row"><kbd>${i18n.t('t_help_lang_kbd')}</kbd><span>${i18n.t('t_help_lang')}</span></div>
      </div>
      <p class="body-small text-on-surface-variant mt-4">${i18n.t('t_help_nodes')}</p>
      <div slot="actions" end style="display:flex;gap:8px;justify-content:flex-end;">
        <m3e-button variant="filled" data-act="ok">${i18n.t('t_ok')}</m3e-button>
      </div>`;
    document.body.appendChild(dlg);
    dlg.querySelector('[data-act="ok"]').addEventListener('click', () => { dlg.close(); });
    dlg.addEventListener('close', () => setTimeout(() => dlg.remove(), 350));
  }
  // 同上：避开本次点击冒泡导致的 scrim 轻关闭
  setTimeout(() => dlg.show());
}

/** 导航栏 HTML 注入（所有页面共享） */
export function injectNav(activeKey) {
  const items = [
    { key: 'dashboard', href: '/', icon: 'dashboard' },
    { key: 'nodes', href: '/nodes', icon: 'list' },
    { key: 'selector', href: '/selector', icon: 'tune' },
    { key: 'settings', href: '/settings', icon: 'settings' },
  ];
  const nav = document.querySelector('[data-nav]');
  if (!nav) return;
  nav.innerHTML = items.map(it => `
    <a href="${url(it.href)}" class="${it.key === activeKey ? 'active' : ''}">
      <span class="material-symbols-rounded">${it.icon}</span>
      <span class="nav-label" data-i18n="nav_${it.key}">${i18n.t('nav_' + it.key)}</span>
    </a>
  `).join('');
  // 重新绑定 ripple + i18n（innerHTML 后 label 需可被语言切换刷新）
  nav.querySelectorAll('a').forEach(a => {
    attachRipple(a);
  });
}

/** 注入 top app bar HTML */
export function injectAppBar(title) {
  const bar = document.querySelector('[data-app-bar]');
  if (!bar) return;
  bar.innerHTML = `
    <div class="brand">
      <div class="logo"><span class="material-symbols-rounded">hub</span></div>
      <span>edt_panel</span>
    </div>
    ${title ? `<span class="title-medium text-on-surface-variant" style="margin-left:8px;">· ${escapeHtml(title)}</span>` : ''}
    <span class="spacer"></span>
    <button class="icon-btn" data-lang-toggle aria-label="切换语言">
      <span class="material-symbols-rounded" data-lang-icon>language</span>
    </button>
    <button class="icon-btn" data-theme-toggle aria-label="切换主题">
      <span class="material-symbols-rounded" data-theme-icon>dark_mode</span>
    </button>
  `;
  // 重新绑定 i18n（injectAppBar 后 icon 重新生成）
  i18n.apply();
}
