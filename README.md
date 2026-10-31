# iptest

一个用于个人自用的跨平台 IP 检测命令行工具，优先检查代理的公网出口，并在终端输出本机网卡 IP、公网出口 IP、估计地理位置和简要信誉结论。

> 当前状态：需求与文档初始化。仓库尚未包含 Go 源代码、`go.mod` 或可执行程序。本文中的检测命令、代理适配和平台支持均为开发计划，尚不能直接运行。

## 项目目标

- 在 Windows、Ubuntu、Arch Linux、CachyOS 和 macOS 上使用同一套检测逻辑。
- 优先检测所选代理的实际公网出口，支持 HTTP CONNECT 和 SOCKS5。
- 地理位置只展示国家／地区、省／州、城市。
- 用简短文字展示 IP 信誉，不生成缺少依据的“纯净度百分比”。
- 免费数据源起步，允许配置个人 API Key 增强信誉查询。
- 后续考虑 Android Termux 适配。

## 输出设计

以下为格式示例，IP 和位置为演示数据，并非实际检测结果：

```text
本机网卡 IP：192.168.1.20（Ethernet）
公网出口 IP：203.0.113.10
估计地理位置：日本 / 东京都 / 东京
IP 纯净度：未发现已知风险记录
```

输出约定：

- 枚举已启用网卡的非回环地址；多网卡按名称分别展示，可能包含虚拟网卡。
- IPv4、IPv6 分别检测，按实际观测到的出口分别展示对应位置和信誉。
- 地理位置缺失的层级显示“未知”。定位结果为 IP 出口的估计位置。
- 正常结果写入标准输出；错误原因写入标准错误，命令通过退出码反映执行状态。
- 位置或信誉查询失败时，保留已经获取的 IP，失败项目显示“未知”并附简短原因。

## 代理选择规则

计划采用以下优先级：

```text
命令行 --proxy
    > 工具保存的代理配置（如已配置）
    > 环境变量代理
    > 可识别的系统静态代理
    > 当前系统网络路径
```

具体行为：

1. 显式支持环境变量代理，包括按请求协议选择的 `HTTP_PROXY`、`HTTPS_PROXY`，以及 `ALL_PROXY` 回退；处理相应小写变量和 `NO_PROXY` 例外。
2. 手动指定代理时，以明确选择为准，不让环境变量的绕过规则意外改变本次检测路径。
3. 自动模式遇到绕过规则时，应明确报告该目标未走所选代理；不会把直连结果冒充代理出口。复杂 PAC 或暂未适配的系统配置提示手动指定代理。
4. 已选代理连接失败、认证失败或超时时，报告失败，不静默切换为直连。
5. 未发现应用层代理时，使用当前系统网络路径；该路径仍可能经过 TUN／VPN。
6. 出口探测及后续查询采用同一套代理策略；获得出口 IP 后，地理位置和信誉接口均显式查询这个 IP。
7. 不同目标或不同时间返回不同出口时，保留差异，避免将不同 IP 的位置和信誉拼成一个结果。

系统静态代理需要按平台适配。Go 标准库不会自动覆盖所有桌面系统代理，`ALL_PROXY` 也需要显式补充处理。

## 命令设计

以下为计划接口，需在程序实现后使用。假设可执行文件已经放入 `PATH`：

```sh
# 自动选择检测路径
iptest

# 明确指定 HTTP 代理
iptest --proxy http://127.0.0.1:7890

# 明确指定 SOCKS5 代理
iptest --proxy socks5://127.0.0.1:1080
```

Windows 从当前目录运行时使用 `.\iptest.exe`；Linux／macOS 从当前目录运行时使用 `./iptest`。

本地配置与 API Key 的具体格式将在配置模块实现时确定。`.gitignore` 预留了本地配置文件的忽略规则，并不代表程序已经支持自动加载这些文件。

## IP 信誉结论

| 结论 | 含义 |
| --- | --- |
| 未发现已知风险记录 | 已成功查询的信誉来源中，暂未发现相关风险记录 |
| 发现风险 | 已查询来源返回滥用举报等风险证据，附简短原因 |
| 未知 | 未配置信誉查询、接口不可用或数据不足 |

“未发现已知风险记录”不保证所有网站都会接受该 IP。使用 VPN／代理、属于机房网络，也不会单独作为存在滥用的结论。

## 数据源候选

| 用途 | 候选来源 | 接入计划 |
| --- | --- | --- |
| 公网出口探测 | [ipify](https://www.ipify.org/) | HTTPS 回显，分别验证 IPv4／IPv6 |
| 估计地理位置 | [ipwhois](https://ipwhois.io/documentation) | 无 Key 免费接口，查询国家、省州、城市 |
| 滥用信誉 | [AbuseIPDB](https://docs.abuseipdb.com/) | 用户自行配置免费 API Key，使用查询接口 |

数据源采用独立适配器，处理超时、缓存、限流和返回字段缺失。接口额度与能力以供应商当前文档为准。查询服务会接收到待查 IP；API Key 和代理认证信息只保留在本机配置中，日志需要脱敏。

## 平台与技术方案

计划使用 Go 实现，优先采用标准库和纯 Go 依赖，减少运行时及系统动态库依赖。

| 系统 | 计划交付形式 | 验证状态 |
| --- | --- | --- |
| Windows | 对应 CPU 架构的 `iptest.exe` | 待实现、待验证 |
| Ubuntu | 对应 CPU 架构的 Linux 可执行文件 | 待实现、待验证 |
| Arch Linux | 对应 CPU 架构的 Linux 可执行文件 | 待实现、待验证 |
| CachyOS | 对应 CPU 架构的 Linux 可执行文件 | 待实现、待验证 |
| macOS | Intel／Apple Silicon 可执行文件 | 待实现、待验证 |
| Android | 后续优先考虑 Termux | 后续评估 |

Linux 各发行版以共用对应架构的构建产物为目标，但仍需分别运行验证。Android 的网卡读取、权限和代理行为需要单独适配。

## 当前文件

```text
iptest/
├── README.md       # 项目范围、开发约定与 GitHub 初始化指南
├── .gitignore      # 构建产物和本地配置忽略规则
└── .gitattributes  # 跨平台文本换行约定
```

## 开发约定

- Go 版本以未来的 `go.mod` 声明为准；模块路径待 GitHub 仓库地址确定后设置。
- 源文件采用 UTF-8 编码；常规文本使用 LF 换行，Go 代码使用 `gofmt`。
- 实现保持精简，按需拆分命令入口、代理、出口、地理位置和信誉查询逻辑。
- 针对代理失败不直连、出口变化、IPv4／IPv6、接口限流及信誉未知等关键行为进行验证。
- 业务逻辑不依赖终端展示层，便于后续平台适配。
- 小步提交，每次提交围绕一个明确变更；提交说明可使用 `docs:`、`feat:`、`fix:`、`test:`、`chore:` 等前缀。
- 只提交可分享的源码、文档和配置模板；本地 API Key、代理凭据及检测日志不纳入版本控制。

Go 模块和源码创建后，再运行以下检查。当前文档阶段无需执行：

```sh
go vet ./...
go test ./...
```

## 开发计划

- [x] 明确个人自用 CLI 的范围和输出格式。
- [x] 建立 README、Git 忽略和换行约定。
- [ ] 初始化 Git 仓库并首次推送 GitHub。
- [ ] 初始化 Go 模块与命令入口。
- [ ] 实现网卡地址读取、手动代理和环境变量代理。
- [ ] 实现平台系统静态代理适配与公网出口探测。
- [ ] 接入免费地理位置查询。
- [ ] 接入可选的信誉查询 API Key。
- [ ] 验证代理失败、网络切换、限流等关键场景。
- [ ] 在目标系统验证并构建可执行文件。
- [ ] 评估 Android Termux 适配。

## 首次建立 Git 仓库并推送 GitHub

以下命令以当前 Windows PowerShell 为例。其他系统请先进入相应的项目目录，再执行 Git 命令。首次推送文档不需要安装 Go。

### 1. 初始化本地仓库

```powershell
Set-Location 'D:\CSWorkSpace\iptest'
git init -b main
```

### 2. 配置提交身份

如果已有合适的 Git 提交身份，可以跳过本步骤。否则将下面的占位内容替换成自己的信息后执行；不带 `--global`，仅影响当前仓库：

```powershell
git config user.name "你的提交署名"
git config user.email "你的GitHub提交邮箱"
```

邮箱可使用 GitHub 已验证的邮箱，或从 [GitHub 邮箱设置](https://github.com/settings/emails) 复制的 `noreply` 邮箱。提交身份与推送时的 GitHub 登录认证是两回事。

### 3. 创建第一次提交

```powershell
git add README.md .gitignore .gitattributes
git diff --cached --stat
git commit -m "docs: initialize project documentation"
```

### 4. 在 GitHub 创建空仓库

1. 登录 GitHub，打开 [创建仓库页面](https://github.com/new)。
2. 仓库名称建议使用 `iptest`；个人自用可选择 Private，也可自行选择 Public。
3. 不添加远程 README、`.gitignore` 或许可证模板，使远程仓库保持为空，便于接收本地首次提交。
4. 创建后，复制该仓库的 HTTPS 地址。

### 5. 关联远程仓库并推送

将下方 `YOUR_USERNAME` 替换为实际的 GitHub 用户名；如果仓库名称不同，也同步替换 `iptest`：

```powershell
git remote add origin https://github.com/YOUR_USERNAME/iptest.git
git remote -v
git push -u origin main
```

使用配置了 Git Credential Manager 的 Git for Windows 时，按浏览器登录提示完成 GitHub 认证。如果终端要求 HTTPS 认证凭据，不能直接使用 GitHub 账号密码；请使用个人访问令牌，或按官方说明配置 SSH。

推送成功后，刷新 GitHub 仓库页面，应能看到这三个文件。可用下列命令检查本地分支与远程跟踪关系：

```powershell
git status
git branch -vv
```

### 6. 后续日常提交

下面以更新 README 为例；源码开发时将文件名替换为本次实际修改的文件：

```powershell
git status
git diff
git add README.md
git diff --cached
git commit -m "docs: update project documentation"
git push
```

在另一台主机开始工作时，先使用实际仓库地址运行 `git clone`；同一分支切换主机继续开发前，保持工作区干净并执行 `git pull --ff-only`。

### 常见问题

- **`Author identity unknown`**：完成上面的本地提交身份配置，再执行提交。
- **`remote origin already exists`**：先查看 `git remote -v`；确需更换地址时使用 `git remote set-url origin 实际仓库地址`。
- **`src refspec main does not match any`**：检查是否已成功创建首次提交，以及当前分支是否为 `main`。
- **`Repository not found` 或认证失败**：检查远程地址、登录账号和仓库权限，私有仓库也需要正确认证。
- **远程已有提交，推送被拒绝**：先检查远程内容和提交历史，合并后再推送；不要用强制推送覆盖已有内容。

官方参考：

- [将本地项目添加到 GitHub](https://docs.github.com/en/migrations/importing-source-code/using-the-command-line-to-import-source-code/adding-locally-hosted-code-to-github)
- [GitHub 命令行认证](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/about-authentication-to-github)
- [Git Credential Manager 与凭据缓存](https://docs.github.com/en/get-started/git-basics/caching-your-github-credentials-in-git?platform=windows)
