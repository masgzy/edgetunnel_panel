<div align="center">

# EDT

**轻量级 VLESS 订阅管理面板 · 单二进制 · Material 3 Expressive**

一个用 Go 编写的自托管订阅管理工具：管理你的 VLESS 节点，生成订阅链接，
对接 [subconverter](https://github.com/tindy2013/subconverter) 输出 Clash / Surge / sing-box 等任意客户端格式。
全部前端资源内嵌于单个可执行文件，解压即用，无需安装数据库、无需 Node.js。

![Go](https://img.shields.io/badge/Go-1.21%2B-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-blue.svg)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20Windows%20%7C%20Android-3C873A)
![UI](https://img.shields.io/badge/UI-Material%203%20Expressive-6750A4)

</div>

---

## 它是什么

EDT 面向「有几个自建节点、想在多台设备上方便地订阅使用」的个人场景，把通常散落在
文本文件和各类脚本里的节点管理工作收进一个 Web 控制台：

- **节点即数据**：节点以 VLESS 分享链接的形式存放在 `data/vless.txt` 中，一条一行，
  可手工编辑、可在控制台增删改查、也可批量导入导出——没有任何私有格式锁定。
- **订阅即链接**：把形如 `https://your-domain/sub?type=main` 的地址填进代理客户端即可。
  订阅内容按需生成，支持 Base64 通用格式，也可通过 subconverter 桥接直接输出
  Clash YAML、sing-box JSON、Surge、Quantumult X 等目标格式。
- **单二进制交付**：Web 控制台（HTML/CSS/JS/PWA 资源）通过 Go `embed` 打进二进制，
  拷贝一个文件 + 一份 `config.yml` 就是完整部署；升级 = 替换二进制重启。

后端仅依赖 Go 标准库与少量成熟三方库（WebSocket、YAML、终端配色），进程内存占用低，
适合跑在 VPS、NAS、软路由乃至 Android Termux 上。

## 与 edgetunnel 生态的关系

EDT 为 [cmliu/edgetunnel](https://github.com/cmliu/edgetunnel)（Cloudflare 边缘隧道）
生态打造：把原本散落在 Workers 面板、优选 IP 列表和订阅转换器之间的管理动作，
收进一个跑在自己服务器上的本地控制台。

| 上游项目 | EDT 如何对接 |
|----------|--------------|
| [cmliu/edgetunnel](https://github.com/cmliu/edgetunnel) | 管理其节点与 UUID；订阅链接供客户端直连 Workers/Pages 入口 |
| [EDT-Pages 型面板](https://github.com/EDT-Pages/EDT-Pages.github.io)（BPB 血统） | `remote.admin_url` 指向 `admin/config.json`：拉取 UUID；顺带读取 CF.Usage 真实用量展示到客户端流量统计；`auth.login_url` 完成 POST password 登录换 auth Cookie |
| [cmliu/WorkerVless2sub](https://github.com/cmliu/WorkerVless2sub) | 优选 IP 列表兼容 `ip:port#备注` ADD 格式，可直接复用 CloudflareSpeedTest 产出 |

未部署远程面板？没关系——不配置 `remote` 时 EDT 完全本地自治，全部功能照常可用。

## 功能一览

**节点管理**
- 单条添加 / 行内编辑 / 搜索过滤 / 批量选择删除
- 从剪贴板或文件批量导入 VLESS 链接（跳过重复 / 覆盖 / 允许重复三种策略）
- 拖拽排序、批量移动到指定位置，顺序即时反映到订阅输出
- 导出完整节点列表为文本文件

**优选 IP**
- 内置优选 IP 列表管理：点选设为当前生效、排序模式、自定义优选条目
- 与订阅生成联动：优选结果自动参与节点生成

**订阅生成**
- 多订阅模板（profiles）：不同域名、UUID 模式（动态 / 静态）、输出格式并存
- UUID 动态模式对接远程面板每日刷新；支持一键清空重置
- 二维码展示订阅链接，手机扫码即用
- mihomo（Clash Meta）配置生成：内置模板 + ACL4SSR ini 解析（ruleset / custom_proxy_group）
- 订阅访问历史记录

**subconverter 桥接（可选）**
- 三种模式：关闭 / 本地二进制 / 远程实例，控制台内一键在线安装本地版
- 安装器具备事务化保护（下载校验失败不影响现有安装）与魔数完整性检查
- 本地模式带看门狗：子进程崩溃自动重启、并发转换限流防打爆

**控制台体验**
- Material Design 3 Expressive 设计语言，桌面端 Navigation Rail / 移动端底部导航自适应
- HCT 动态主题色：选一个种子色，整套明暗色板实时重算并持久化
- 明暗主题三态切换（浅色 / 深色 / 跟随系统），中英双语一键切换
- PWA：可安装到主屏，资源 ETag + gzip 高效缓存
- 备份 / 恢复：一键打包 `data/` 目录为 zip 下载，或上传 zip 恢复（含路径穿越防护）
- 设置页内置 config.yml 编辑器与运行日志查看器（WebSocket 实时推送）

## 快速开始

### 1. 下载

到 [Releases](../../releases) 页面下载对应平台的压缩包，命名规则
`edt_<os>_<arch>[_<变体>]`，例如：

| 文件 | 适用环境 |
|------|----------|
| `edt_linux_amd64.tar.gz` | 常见 x86_64 Linux（VPS / NAS） |
| `edt_linux_arm64.tar.gz` | ARM64 Linux（树莓派 4/5、ARM 云主机） |
| `edt_linux_armv7.tar.gz` | 32 位 ARM（armv5/armv6/armv7 按设备选择） |
| `edt_linux_mipsle.tar.gz` | MIPS 软浮点路由器（mips/mipsle 固定 softfloat） |
| `edt_darwin_arm64.tar.gz` | Apple Silicon macOS |
| `edt_windows_amd64.zip` | 64 位 Windows |
| `edt_android_arm64.tar.gz` | Android Termux 等终端环境 |

每个 Release 附 `checksums.txt`（SHA256），建议校验后再使用：

```bash
sha256sum -c checksums.txt --ignore-missing
```

以 Linux amd64 为例：

```bash
tar -xzf edt_linux_amd64.tar.gz
chmod +x edt
./edt
```

> 首次不带配置启动时，EDT 会在当前目录生成一份带注释的 `config.yml` 模板然后退出，
> 编辑后再运行即可。也可以先 `cp config.example.yml config.yml` 手动准备。

### 2. 最小配置

编辑 `config.yml`，绝大多数开箱字段可保持默认，建议核对以下三项：

```yaml
auth:
  login_password: CHANGE_ME        # 控制台登录口令，务必修改！留空则完全关闭鉴权

remote:
  control_domain: example.com      # 你的服务域名（用于订阅 SNI 与链接生成）
  admin_url: https://example.com/admin/config.json   # 远程面板查询地址（UUID 服务依赖）

app:
  port: 5001                       # 控制台监听端口
```

### 3. 运行

```bash
./edt -c config.yml
```

常用命令行参数（均可省略，优先级高于配置文件）：

| 参数 | 说明 |
|------|------|
| `-c <path>` | 配置文件路径（默认 `./config.yml`） |
| `-H` / `-p` | 覆盖监听地址 / 端口 |
| `--sc-mode off\|local\|remote` | subconverter 桥接模式 |
| `--sc-port <n>` | 本地 subconverter 监听端口（默认 25500） |
| `--no-color` | 关闭终端彩色输出 |

浏览器打开 `http://<host>:5001`，输入 `login_password` 登录控制台。`Ctrl+C` 优雅退出，
正在运行的 subconverter 子进程会被一并回收。

### 4. 启用 HTTPS（生产环境建议）

控制台本身无需 TLS 即可工作，但公网部署强烈建议置于反向代理之后（Nginx / Caddy 等），
一是加密流量，二是让会话 Cookie 自动启用 `Secure` 属性。要点：

```nginx
location / {
    proxy_pass http://127.0.0.1:5001;
    proxy_set_header Host $host;
    proxy_http_version 1.1;                      # WebSocket 需要
    proxy_set_header Upgrade $http_upgrade;      # /ws 实时日志走同端口同路径
    proxy_set_header Connection "upgrade";
}
```

只需转发一个端口；日志 WebSocket 与页面同源同端口，无需额外配置。

## subconverter 桥接

EDT 原生输出 Base64 / mihomo 格式订阅。若客户端需要 Clash、Surge、sing-box、
Quantumult X 等格式，可启用 subconverter 桥接，在 `config.yml` 中配置：

```yaml
subconverter:
  mode: local          # off=禁用 | local=本地二进制 | remote=远程地址
  bin: bin/subconverter/subconverter
  port: 25500
  # remote: https://your-subconverter.example.com   # mode=remote 时使用
```

**安装本地二进制**（二选一）：

```bash
# 方式一：脚本安装（自动识别架构）
./install_subconverter.sh

# 方式二：登录控制台 → 设置 → subconverter 桥接 → 在线安装
```

默认下载源指向社区维护的 [asdlokj1qpi233/subconverter](https://github.com/asdlokj1qpi233/subconverter)
构建（完整支持 VLESS / Hysteria2 节点；官方 tindy2013 版本暂不支持 VLESS 解析）。
如需更换下载源，编辑 `scbase.txt`（支持 `{arch}` 占位符）。

> 桥接说明：为让代理客户端无需登录即可拉取订阅，`/sub` 与 `/convert` 端点不对
> 控制台会话做鉴权——订阅链接中的 UUID 即访问凭据。请像保管密码一样保管订阅链接。

## 鉴权与安全

- **口令门**：`auth.login_password` 非空时，所有页面与 API 需登录；留空则完全关闭鉴权
  （仅建议纯内网使用）。环境变量 `EDT_LOGIN_PASSWORD` 优先于配置文件。
- **会话**：HMAC-SHA256 签名 Cookie，7 天有效，HttpOnly + SameSite=Lax；
  经 HTTPS 反代访问时自动附加 `Secure`。
- **防爆破**：登录接口按 IP 限速，5 分钟内失败 8 次即临时封禁。
- **数据安全**：备份恢复接口内置 zip 路径穿越（Zip Slip）防护；请求体大小限制；
  安全响应头；subconverter 安装源拒绝环回 / 私网 / 链路本地地址（防 SSRF）。
- **忘记口令**：编辑 `config.yml` 中 `login_password` 后重启服务即可。

## 从源码构建

工具链要求：**Go 1.21 及以上**（无任何前端构建步骤，Node.js 不参与编译）：

```bash
git clone https://github.com/masgzy/edgetunnel_panel.git
cd edgetunnel_panel
go build -trimpath -ldflags="-s -w" -o edt .
```

交叉编译同样简单（默认关闭 CGO）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o edt_linux_arm64 .
```

## 自动化发布

仓库通过 GitHub Actions 实现两条流水线：

- **CI**：每次 push / PR 在 Linux、macOS、Windows 三平台跑构建与静态检查，
  并以 `go.mod` 声明的 Go 底线版本常驻回归，保证最低工具链兼容性。
- **Release**：推送 `v*` 形式的 tag 后自动交叉编译发布，覆盖 16 个目标：
  `android_arm64`、`darwin_amd64/arm64`、`linux_386/amd64/arm64`、
  `linux_armv5/armv6/armv7`、`linux_mips/mips64/mips64le/mipsle`、
  `windows_386/amd64/arm64`。产物为可复现归档（tar 归零时间戳），附统一 SHA256 清单，
  tag 含 `-`（如 `v1.0.0-rc1`）会自动标记为预发布。

发布工作流遵循最小权限原则：整体仅 `contents: read`，仅发布作业提升为 `contents: write`。

## 目录结构

```text
edt/
├── main.go                  # 入口：CLI 解析、装配、优雅退出、subconverter 看门狗
├── internal/
│   ├── config/              # config.yml 加载与默认值
│   ├── module/              # ACL4SSR ini 解析、mihomo 配置、国旗标注等纯逻辑
│   ├── service/             # 节点存储、订阅生成、UUID、优选 IP、统计
│   ├── ui/                  # 终端配色输出
│   └── server/
│       ├── server.go        # 路由与处理器（embed all:web 内嵌前端）
│       ├── webauth.go       # 登录鉴权与会话
│       ├── ws.go            # 实时日志 WebSocket
│       ├── downloader.go    # subconverter 事务化下载安装器
│       └── web/             # 控制台前端（HTML/CSS/JS/PWA 资源）
├── config.example.yml       # 带注释的配置模板
├── scbase.txt               # subconverter 下载源模板（{arch} 占位）
├── install_subconverter.sh  # subconverter 命令行安装脚本
└── .github/workflows/       # CI 与 Release 工作流
```

运行时自动生成的 `data/`（节点库、统计、日志缓存）与 `bin/subconverter/`
（第三方程序）不进入版本库，由 `.gitignore` 排除。

## 常见问题

<details>
<summary><b>Android 上能用吗？</b></summary>

可以。Release 提供 `android_arm64` 专用包，配合 Termux 直接运行；也可使用
`linux_arm64` 包尝试。若要在 Termux 里使用本地 subconverter，推荐用 `android_arm64`
包，其安装器会优先拉取 aarch64 架构的二进制。
</details>

<details>
<summary><b>订阅链接给谁用？安全吗？</b></summary>

给代理客户端用。`/sub` 以 URL 中的 UUID 作为访问凭据，拿到链接即可获取全部节点内容，
因此不要公开传播；泄露后可在控制台「清空 UUID」重置动态模式凭据。
</details>

<details>
<summary><b>转换时报 "No nodes were found"</b></summary>

多为 subconverter 后端不支持 VLESS 所致（常见于 tindy2013 官方构建）。使用本项目
默认下载源重新安装即可，或在远程模式下换用支持 VLESS 的实例。
</details>

<details>
<summary><b>如何备份数据？</b></summary>

控制台 → 设置 → 备份：打包 `data/` 目录为 zip 下载；恢复时上传该 zip 即可整目录还原。
也可以直接停服后拷贝 `data/` 目录，效果等同。
</details>

<details>
<summary><b>端口可以改吗？会被占用怎么办？</b></summary>

改 `config.yml` 的 `app.port`，或运行时 `-p 8080` 覆盖。subconverter 本地端口默认
25500，同样可通过配置或 `--sc-port` 调整。
</details>

<details>
<summary><b>控制台字体图标加载失败？</b></summary>

控制台不依赖任何 Google 服务域名，Material Symbols 字体走公共镜像加载，离线环境
亦可正常使用（图标缺失不影响功能）。
</details>

## 已知限制

- 页面切换动效基于 View Transitions API：Firefox / Safari 自动降级为即时切换，
  不影响任何功能。
- `linux_mips` / `linux_mipsle` 目标固定使用 softfloat（软浮点），兼容面最广但性能略低。
- 控制 i18n 覆盖界面框架与常用文案，个别长尾提示语可能仍显示中文。

## 贡献

欢迎 Issue 与 Pull Request：

1. Fork 并创建特性分支（`git checkout -b feature/amazing`）
2. 提交前确保 `gofmt` / `go vet` / `go build` 全部通过
3. 附上变更说明与验证方式

## 许可证

本项目以 [MIT License](LICENSE) 发布。

第三方组件致谢：

| 组件 | 用途 | 许可 |
|------|------|------|
| [gorilla/websocket](https://github.com/gorilla/websocket) | 实时日志 WebSocket | BSD-2-Clause |
| [alecthomas/kong](https://github.com/alecthomas/kong) | 命令行解析 | MIT |
| [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | 终端配色 | MIT |
| [gopkg.in/yaml.v3](https://gopkg.in/yaml.v3) | YAML 解析 | Apache-2.0 / MIT |
| [@m3e/web](https://github.com/matraic/m3e) | Material 3 Expressive Web 组件 | MIT |
| [material-color-utilities](https://github.com/material-foundation/material-color-utilities) | HCT 动态主题色算法 | Apache-2.0 |

[subconverter](https://github.com/tindy2013/subconverter) 为独立第三方程序，
由用户按需自行安装、独立授权，不随本项目分发。
