// Web 中英双语(i18n)轻量方案:
// 1) 词典 DICT(zh → en),按"最长匹配 + 仍含中文片段"替换 DOM 文本/占位;
// 2) 语言切换后 location.reload() 全量生效,localStorage 记忆;
// 3) Element Plus 组件文案由 <el-config-provider> 语言包接管(见 App.vue)。
const KEY = 'zfsmgr_lang'
export function lang() { return localStorage.getItem(KEY) || 'zh' }
export function setLang(v) {
  localStorage.setItem(KEY, v === 'en' ? 'en' : 'zh')
  location.reload()
}

// —— 词典:整句短语 + 高频词元(自动去重,按需补充) ——
const D = {
  // 登录 / 框架
  '存储管理': 'Storage Management',
  '存储节点': 'Storage nodes',
  '在线': 'Online',
  '离线': 'Offline',
  '容量已用 / 总': 'Used / total capacity',
  '活动会话': 'Active sessions',
  '主机 / 映射': 'Hosts / mappings',
  '登录以继续': 'Sign in to continue',
  '存储管理员控制台 · 卷 / 快照 / iSCSI 映射': 'Storage admin console · Volumes / Snapshots / iSCSI mappings',
  '退出登录': 'Sign out',
  '切换到浅色': 'Switch to light',
  '切换到深色': 'Switch to dark',
  '能力探测 capabilities': 'Capabilities',
  '原始 capabilities JSON': 'Raw capabilities JSON',
  '操作系统': 'OS', '注册时间': 'Registered', '最后心跳': 'Last heartbeat',
  'Agent 地址': 'Agent address', 'Agent 版本': 'Agent version', 'ZFS 版本': 'ZFS version',
  '名称': 'Name', '池名': 'Pool name', '卷名': 'Volume name', '数据集名': 'Dataset name',
  '节点': 'Nodes', '主机': 'Hosts', '主机档案': 'Host profile', '主机(Initiator 档案)': 'Hosts (initiators)',
  '卷': 'Volumes', '快照': 'Snapshots', '映射': 'Mappings', '会话': 'Sessions',
  '存储池': 'Pools', '新建池': 'New pool', '新建存储池': 'Create pool',
  '容量': 'Size', '已用': 'Used', '卷数': 'Volumes', '快照数': 'Snapshots',
  '状态': 'Status', '类型': 'Type', '时间': 'Time', '结果': 'Result', '资源': 'Resource',
  '动作': 'Action', '参数': 'Params', '详情': 'Detail', '资源类型': 'Resource type',
  '操作': 'Actions', '创建': 'Create', '新建': 'New', '删除': 'Delete', '移除': 'Remove',
  '确认': 'Confirm', '取消': 'Cancel', '保存': 'Save', '应用': 'Apply', '刷新': 'Refresh',
  '查询': 'Query', '重置': 'Reset', '管理': 'Manage', '属性': 'Properties',
  '压缩': 'Compression', '重删': 'Dedup', '重删 dedup': 'Dedup',
  '继承': 'Inherit', '默认': 'Default', '可选': 'Optional', '启用': 'Enable',
  '挂载': 'Mount', '挂载卷': 'Attach volume', '回滚': 'Rollback', '克隆': 'Clone',
  '快照与回滚': 'Snapshots & rollback', '回滚到快照': 'Rollback to snapshot',
  '创建快照': 'New snapshot', '创建时间': 'Created', '占用': 'Used by',
  '格式化': 'Format', '格式化(破坏性)': 'Format (destructive)',
  '扩容/缩容': 'Resize', '调整容量': 'Resize volume', '目标容量': 'Target size', '调整': 'Resize',
  '危险区': 'Danger zone', '只读视图': 'Read-only view',
  '文件系统': 'Filesystem', '文件系统 dataset 以 mountpoint=none 托管(平台不负责挂载),可作共享数据目录/快照源':
    'Filesystem datasets are managed with mountpoint=none (no auto mount). Good for shared data dirs / snapshot sources.',
  '数据集 (zvol / zfs 文件系统)': 'Datasets (zvol / ZFS filesystem)',
  'zvol 卷': 'zvol', '卷 (zvol)': 'Volumes (zvol)', '未格式化': 'Unformatted',
  '已映射': 'Mapped', '本机已挂载': 'Mounted locally', '已用卷': 'Used',
  '克隆来源': 'Cloned from', '压缩率': 'Compression ratio', '记录大小 recordsize': 'Record size',
  '块大小': 'Block size', 'Sparse 卷': 'Sparse volumes',
  'zfs create -s(属性不可见)': 'via zfs create -s (property hidden)',
  '映射给主机': 'Map to host', '解除映射': 'Unmap',
  '未映射。可在「主机 → 分配卷」或此处直接映射:': 'Not mapped. Map from Hosts → Allocate, or here:',
  '选择主机': 'Select host', '主机:': 'Host: ', '选择未映射 zvol': 'Select unmapped zvol',
  '分配并映射': 'Allocate & map', '分配卷(自动建 Target + ACL,返回连接参数)':
    'Allocate volume (auto Target + ACL, returns connection info)',
  '删除不可恢复,确认?': 'Deletion is irreversible. Continue?',
  '需键入名称二次确认': 'Type the name to confirm',
  '需键入卷名确认;已映射或已挂载时禁用': 'Type volume name to confirm; disabled while mapped or mounted',
  '需键入卷名二次确认': 'Type the volume name to confirm',
  '回滚将丢弃该快照之后写入的全部数据,且仅支持最新快照。': 'Rollback discards data written after this snapshot; only the newest snapshot can be rolled back.',
  '确认回滚': 'Confirm rollback',
  '创建(清空所选磁盘!)': 'Create (wipe selected disks!)',
  '全部节点': 'All nodes', '名称 (dataset)': 'Name (dataset)', '类型 / 文件系统': 'Type / FS',
  '新快照名,如 backup-20260906': 'Snapshot name, e.g. backup-20260906',
  '属性配置(compression / dedup)': 'Properties (compression / dedup)',
  '压缩:': 'Compression:', '重删:': 'Dedup:',
  '重删开启需键入名称二次确认;压缩即时生效(新写入)': 'Dedup requires name confirmation; compression applies to new writes.',
  '文件系统(仅执行 mkfs,不负责挂载)': 'Filesystem (mkfs only, no mount)',
  'iSCSI 映射': 'iSCSI mapping',
  '源卷(带快照)': 'Source volume (has snapshots)', '选择带快照的 zvol': 'Select zvol with snapshots',
  '选择要克隆的快照': 'Select snapshot to clone', '新卷名': 'New volume name',
  '克隆出的可写新卷名,如 vol-clone-01': 'Writable clone name, e.g. vol-clone-01',
  '容量/压缩/块大小等继承自快照;克隆卷可写,可继续格式化或映射':
    'Size/compression/block size are inherited from the snapshot; the clone is writable and can be formatted or mapped.',
  '新建空白卷': 'New blank volume', '从快照克隆': 'Clone from snapshot',
  '如 2G / 512M': 'e.g. 2G / 512M', '可选 4k/16k/128k,默认继承': '4k/16k/128k optional, inherit by default',
  '如 vol-mysql01': 'e.g. vol-mysql01', '如 data-01 / logs': 'e.g. data-01 / logs',
  '如 4G(缩容为破坏性操作,需二次输入卷名)': 'e.g. 4G (shrinking is destructive, type the name again)',
  // 池
  '数据 vdev 组(1~n 组,每组可不同类型)': 'Data vdev groups (1..n, per-group type)',
  '组内设备数:stripe ≥1、mirror ≥2、raidz ≥3、raidz2 ≥4、raidz3 ≥5(以实际校验为准)':
    'Devices per group: stripe ≥1, mirror ≥2, raidz ≥3, raidz2 ≥4, raidz3 ≥5 (final check by zpool)',
  '附加设备(可选)': 'Additional devices (optional)',
  '建池参数': 'Pool options',
  'slog (log)': 'slog (log)',
  '日志盘(建议 1~2 块,可用小容量 SSD)': 'Log device (1-2 recommended, small SSD ok)',
  'cache': 'cache',
  'L2ARC 缓存盘(可选)': 'L2ARC cache device (optional)',
  '条带 stripe': 'stripe', '镜像 mirror': 'mirror', '选择该组设备': 'Select devices for this group',
  '+ 添加 vdev 组': '+ Add vdev group', '移除': 'Remove',
  '4Kn 盘选 12;512e 盘常选 12~13': '4Kn: 12; 512e: usually 12-13',
  '卷/数据库负载建议 16k~64k': 'Volumes / DB workloads: 16k-64k recommended',
  '重删显著增加内存开销,请确认内存充足': 'Dedup heavily increases memory usage; ensure enough RAM',
  '作用于池根文件系统(新建子数据集默认继承)': 'Applies to the pool-root filesystem (child datasets inherit by default)',
  '继承(上一层)': 'Inherit (parent)', 'lz4(默认推荐)': 'lz4 (recommended)',
  '默认 128k': 'Default 128k', 'off(默认)': 'off (default)', '当前:': 'Current: ',
  '开启重删会显著增加内存与 CPU 开销,建议先在小数据集验证。': 'Dedup adds significant RAM/CPU cost; test on a small dataset first.',
  '池状态': 'Pool status', '错误计数': 'Error count', '扫描 scan': 'Scan',
  '无进行中扫描': 'No scan in progress', '通过 · 错误': 'passed · errors',
  'vdev 拓扑与错误': 'vdev topology & errors', '名称 / 拓扑层级': 'Name / level',
  '读写错误': 'R/W errors', '校验错误': 'Checksum errors', '已分配 / 容量': 'Used / size',
  '原始 zpool status JSON': 'Raw zpool status JSON',
  '销毁': 'Destroy', 'Scrub': 'Scrub', '提示:选择整块空白盘(无文件系统),将整盘建池':
    'Pick blank whole disks (no filesystem); the whole disk becomes the pool.',
  '已过滤 N 块设备:含文件系统、已有分区或处于挂载状态': 'filtered out devices with filesystem / partitions / mounts',
  '已过滤': 'filtered out:', '无可用磁盘': 'No usable disks',
  // 目标
  '新建 Target': 'New target', '新建 iSCSI Target(一卷一目标)': 'New iSCSI Target (one volume per target)',
  '选择尚未映射的 zvol(可选)': 'Choose unmapped zvol (optional)',
  '留空自动生成 iqn.YYYY-MM.com.ustc:...': 'Leave blank to auto-generate iqn.YYYY-MM.com.ustc:...',
  'LUN': 'LUN', '会话数': 'Sessions', 'ACL': 'ACL', '映射管理': 'Managed by mapping', '手工': 'Manual',
  'ACL(授权 Initiator / CHAP)': 'ACL (authorized initiators / CHAP)',
  '活动会话(实时)': 'Active sessions (live)', '断开': 'Disconnect',
  '选择主机 IQN 或输入': 'Select host IQN or type', '来源 IP': 'Source IP',
  '来源': 'Source', 'CHAP 用户': 'CHAP user', '≥12位': '≥12 chars',
  'zvol / 设备': 'zvol / device', 'backstore WWN (客户端 by-id)': 'backstore WWN (client by-id)',
  '挂载另一卷': 'Attach another volume', '挂载 LUN': 'Attach LUN',
  '添加 ACL': 'Add ACL', '删除该 Target(存在会话/映射时会被拒绝)?': 'Delete this Target? (refused while sessions/mappings exist)',
  '删除 Target': 'Delete target', 'iSCSI 目标': 'iSCSI targets',
  '删除该 Target': 'Delete this target',
  // 主机
  '新建主机档案': 'New host profile', '新建主机': 'New host', '系统': 'System', '备注': 'Notes',
  '如 backup-server-01': 'e.g. backup-server-01', '如 Debian 13 / Windows 2019': 'e.g. Debian 13 / Windows 2019',
  '已映射卷': 'Mapped volumes', '录入': 'Add',
  'Initiator(录入本主机的 iSCSI IQN;CHAP 可选)': 'Initiators (host iSCSI IQNs; CHAP optional)',
  '登录': 'Sign in', '口令': 'Password',
  '管理员': 'Administrator', '运维员': 'Operator', '只读': 'Read-only',
  '审计': 'Audit', '无告警': 'No alerts', '告警': 'Alerts', '最近操作': 'Recent activity',
  '新建 Target': 'New target', '管理': 'Manage', '名称': 'Name',
  '用户名': 'Username',
  '创建时间': 'Created',
  '卷名': 'Volume name', '名称 (dataset)': 'Name (dataset)',
  '选择该组设备': 'Select devices for this group',
  '选择主机 IQN 或输入': 'Select host IQN or type',
  // 映射
  '映射一览 (卷 ↔ 主机)': 'Mappings (volume ↔ host)',
  '浅色': 'Light', '深色': 'Dark', '跟随系统': 'System', '主题:': 'Theme: ',
  '实时监控': 'Monitoring', 'CPU 使用率': 'CPU usage', '内存使用率': 'Memory usage',
  '网络吞吐': 'Network throughput', '磁盘 IO': 'Disk IO', '采样间隔 12s': 'sample interval 12s',
  '指标采集中(约需 1 分钟积累两个采样点)': 'Collecting metrics (needs ~1 minute for two samples)',
  '启用自动挂载': 'Enable auto mount', '未挂载(none),不可共享': 'not mounted (none), not sharable',
  '待挂载': 'pending mount', '已挂载': 'mounted', '选择文件系统数据集': 'Select filesystem dataset',
  '该节点暂无文件系统数据集,请先在「卷 → 文件系统」创建。': 'No filesystem dataset on this node; create one under Datasets → Filesystem first.',
  '共享': 'Shares', '文件共享 (NFS / SMB-CIFS)': 'File shares (NFS / SMB-CIFS)',
  '新建共享': 'New share', '协议': 'Protocol', '共享名': 'Share name',
  '新建文件共享(创建在指定存储池的数据集上)': 'New file share (on a dataset inside a pool)',
  '存储池数据集': 'Pool dataset', '共享目录(数据集)': 'Shared directory (dataset)',
  '客户端': 'Clients', '访问权限': 'Access', '同步模式': 'Sync mode', 'root 用户': 'Root user',
  '允许匿名访问': 'Allow guest', '允许用户': 'Allowed users', '挂载点': 'Mountpoint',
  '不挂载 none': 'Not mounted (none)', '自动 /pool/name': 'Auto /pool/name', '自定义路径': 'Custom path',
  '支持的存储能力': 'Supported storage capabilities', '存储池布局': 'Pool layouts',
  '数据集特性': 'Dataset features', '块协议': 'Block protocols', '文件协议': 'File protocols',
  '卷文件系统': 'Volume filesystems', '节点能力': 'Node capabilities',
  // 审计
  '审计日志': 'Audit logs', '成功': 'ok', '失败': 'failed',
  // 用户
  '用户与角色(admin)': 'Users & roles (admin)', '角色': 'Role', '新建用户': 'New user',
  'admin(全部)': 'admin (all)', 'operator(变更)': 'operator (changes)', 'viewer(只读)': 'viewer (read-only)',
  '修改用户': 'Edit user', '新口令': 'New password', '留空不修改': 'Leave blank to keep',
  '改角色/口令': 'Edit role/password', '创建用户': 'Create user', '禁用': 'Disable', '启用用户': 'Enable user',
  // 概览/仪表盘
  '概览': 'Overview', '告警': 'Alerts', '最近操作': 'Recent activity', '用户': 'Users',
  // 节点页顶栏等
  '存储节点 (Agent 启动后自动注册)': 'Storage nodes (auto-registered by Agents)',
  '在线': 'Online', '离线': 'Offline', '注册时间': 'Registered',
  '节点列表': 'Nodes', '节点详情': 'Node detail',
  'ZFS JSON 输出': 'ZFS JSON output', 'mkfs 工具': 'mkfs tools', 'FC 目标模式': 'FC target mode',
  '支持 (OpenZFS ≥ 2.3)': 'Supported (OpenZFS ≥ 2.3)',
  '传输': 'Transport', '传输类型': 'Transport', '目标 WWPN': 'Target WWPN',
  '新建 Target(iSCSI / FC)': 'New Target (iSCSI / FC)',
  'Target(iSCSI / FC)': 'Targets (iSCSI / FC)',
  'Initiator(iSCSI IQN / FC WWPN)': 'Initiators (iSCSI IQN / FC WWPN)',
  'Initiator 标识(录入 iSCSI IQN 或 FC WWPN)': 'Initiator identity (iSCSI IQN or FC WWPN)',
  '客户端标识': 'client identity', 'FC 不可用:': 'FC unavailable: ',
  'FC 无需 CHAP': 'CHAP not required for FC',
  '未检测到兼容 QLogic HBA': 'no compatible QLogic HBA detected',
  // 动态片段(词元)
  '空闲': 'free', '个快照': 'snapshots', '加密': 'encrypted',
  '本机已挂载': 'mounted locally',
  '高亮': 'highlight',
  // Element 空状态等由语言包处理;以下为插值提示
  '请输入': 'Enter ', '以确认删除': 'to confirm deletion',
  '以确认销毁': 'to confirm destruction', '以确认': 'to confirm',
  '确认删除': 'Confirm delete',
  '卷详情': 'Volume detail', '目标详情': 'Target detail', '节点详情-能力探测(结构化展示)': 'Node detail — capabilities (structured)',
  '格式化将清除卷上全部数据,请输入卷名': 'Formatting erases all data. Type volume name',
  '卷仍映射为 LUN 强校验请先在卷详情解除映射': 'Volume is mapped as a LUN; unmap it first',
  '复制到其他节点': 'Copy to another node',
}
// 归一化键(统一空白)
const DICT = {}
for (const [k, v] of Object.entries(D)) DICT[k.replace(/\s+/g, ' ').trim()] = v
const KEYS = Object.keys(DICT).sort((a, b) => b.length - a.length)

// 将任意文本中的中文词条替换为英文(最长优先,逐词元)
export function trText(text) {
  if (typeof text !== 'string' || lang() !== 'en') return text
  let out = text
  for (const k of KEYS) {
    const v = DICT[k]
    if (v === undefined || !out.includes(k)) continue
    out = out.split(k).join(v)
  }
  return out
}

// 扫描并翻译 DOM(#app 内文本节点与 placeholder/title)
export function applyI18n() {
  if (lang() !== 'en') return
  const root = document.body
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  const nodes = []
  while (walker.nextNode()) nodes.push(walker.currentNode)
  for (const n of nodes) {
    const parent = n.parentElement
    if (!parent || ['SCRIPT', 'STYLE', 'TEXTAREA', 'INPUT', 'OPTION'].includes(parent.tagName)) continue
    const t = (n.nodeValue || '').replace(/\s+/g, ' ')
    if (!/[\u4e00-\u9fff]/.test(t)) continue
    const nt = trText(t)
    if (nt !== n.nodeValue) n.nodeValue = nt
  }
  root.querySelectorAll('[placeholder]').forEach(el => {
    if (/[\u4e00-\u9fff]/.test(el.getAttribute('placeholder') || '')) {
      el.setAttribute('placeholder', trText(el.getAttribute('placeholder')))
    }
  })
  root.querySelectorAll('[title]').forEach(el => {
    const v = el.getAttribute('title')
    if (v && /[\u4e00-\u9fff]/.test(v)) el.setAttribute('title', trText(v))
  })
}

// 错误码 → 英文模板({name} 占位由 params 替换);供 api.js 在英文模式下渲染
export const ERR_KEYS = {
  'validation.invalid': 'Invalid input: {detail}',
  'resource.not_found': 'Not found: {resource}',
  'resource.conflict': 'Conflict: {detail}',
  'resource.duplicate': 'Already exists: {name}',
  'auth.unauthorized': 'Unauthorized',
  'auth.forbidden': 'Permission denied (requires {role})',
  'auth.rate_limited': 'Too many attempts. Retry in {minutes} minute(s)',
  'node.offline': 'Node is offline',
  'transport.fc_unavailable': 'FC unavailable: {reason}',
  'confirm.require_name': 'Type "{name}" to confirm',
  'target.zvol_only': 'Only zvol can be used as LUN backing store',
  'agent.error': 'Agent error',
  'internal.error': 'Internal error',
}

export function trErr(key, params, fallback) {
  const t = ERR_KEYS[key]
  if (!t) return fallback
  return t.replace(/\{(\w+)\}/g, (_, k) => (params && params[k] !== undefined ? String(params[k]) : '{' + k + '}'))
}

export function initI18n() {
  document.documentElement.lang = lang()
  if (lang() === 'en') {
    const scan = () => applyI18n()
    requestAnimationFrame(scan)
    window.addEventListener('load', () => { scan(); setInterval(scan, 2000) })
    document.addEventListener('click', () => requestAnimationFrame(scan))
  }
}
