# edifier-control

[![Release](https://img.shields.io/github/v/release/4285f4/edifier-control?color=blue)](https://github.com/4285f4/edifier-control/releases/latest)
[![License](https://img.shields.io/github/license/4285f4/edifier-control)](LICENSE)

在 Windows 上通过蓝牙 BLE 控制漫步者（Edifier）降噪耳机的**音效模式**，解决"每次开机默认降噪、想要的标准模式又老是消失"的问题。

单文件可执行程序，无需安装运行时、无需手机 App。**[下载最新版本](https://github.com/4285f4/edifier-control/releases/latest)**

```
edifier-control.exe standard
```

---

## 它解决什么问题

漫步者 W820NB 系列（以及部分同族型号）有三个固件层面的毛病：

1. **不记忆音效模式**：每次开机都回落到「降噪」。降噪带来的耳压对不少人并不舒服。
2. **「标准模式」不在按键循环里**：出厂只循环「降噪 / 环境声」两档，想用标准模式得先在手机 App 里勾选。
3. **勾选了也保不住**：断电重开后经常被打回两档，社区里有用户反馈"十次有七八次丢失"。

而耳机 USB 接入电脑时**没法用 USB 控制**——它在 USB 上只暴露两个接口：

| 接口 | 类型 | 能力 |
| --- | --- | --- |
| MI_00 | USB Audio | 音频流 |
| MI_03 | HID Consumer Control | `InputReport=3`，**`OutputReport=0` / `FeatureReport=0`** |

HID 这一路是单向的：耳机把音量键、播放键的事件发给电脑，主机没有任何通道能反向下发命令。所以想改模式，只能走蓝牙。

## 原理

耳机的 BLE 控制通道与音频链路相互独立：即使你正在用 USB 听歌，也依然可以经由 BLE 下发控制命令。

协议帧格式：

```
主机 → 耳机:  AA | len | payload | crc_hi | crc_lo
耳机 → 主机:  BB | len | payload | crc_hi | crc_lo   （状态/数值应答）
              CC | len | payload | crc_hi | crc_lo   （确认应答）
```

`crc = (0x2019 + 帧内此前所有字节之和) & 0xFFFF`，大端写出。

用到的命令：

| Payload | 含义 | 应答 |
| --- | --- | --- |
| `CC` | 查询当前模式 | `BB 03 CC <mode> <vol>` |
| `C1 01` / `C1 02` / `C1 03` | 设为 标准 / 降噪 / 环境声 | `BB 03 C1 <mode> <vol>` |
| `F0 0A` | 查询按键循环掩码 | `BB 03 F0 0A <mask>` |
| `F1 0A <mask>` | 设置按键循环掩码 | `BB 03 F1 0A <mask>` |

其中 `mask` 为位域：`bit0` 标准、`bit1` 降噪、`bit2` 环境声。出厂常见的 `0x06` 就是"标准模式不在循环里"，`0x07` 才是三档齐全。

## 用法

首次运行会自动扫描附近的漫步者设备：只找到一台就直接连接，找到多台会列出来让你选，连接成功后把地址写入同目录的 `edifier.json`，之后直接复用。

```
edifier-control.exe status      查看当前模式与按键循环
edifier-control.exe standard    切到标准模式
edifier-control.exe anc         切到降噪模式
edifier-control.exe ambient     切到环境声模式
edifier-control.exe fix         把按键循环修复为三档（标准 + 降噪 + 环境声）
edifier-control.exe discover    扫描并打印设备地址
edifier-control.exe guard       常驻：连上即校准，断线自动重连
```

选项可写在命令前或命令后：

```
-addr MAC     直接指定设备地址（跳过配置文件，仅本次生效）
-discover     忽略配置文件，强制重新扫描
-log PATH     guard 模式下把日志写入文件
```

### 关于 `fix`

`F1 0A 07` 这一条能把「标准模式」重新塞回按键循环，之后你直接用耳机上的按键就能在三档之间切换，不必再打开手机 App。

```
$ edifier-control.exe fix
当前按键循环: 降噪 + 环境声 (0x06)
已恢复三档循环: 标准 + 降噪 + 环境声 (0x07)
```

## 开机自动切到标准模式

如果耳机常年插在电脑上，最省事的方式是**开机时跑一次** `standard`，不需要常驻进程。

**任务计划程序**：创建任务 → 触发器「登录时」→ 操作「启动程序」：

```
程序：  D:\path\to\edifier-control.exe
参数：  standard
起始于：D:\path\to
```

**或启动文件夹**：`Win+R` 输入 `shell:startup`，放一个指向下列命令的快捷方式：

```
"D:\path\to\edifier-control.exe" standard
```

> 说明：固件不记忆模式，因此严格意义上的"开机即为标准"做不到，只能在登录后 1～3 秒内完成切换，实际基本无感。
> 如果你希望连耳机中途开机也能自动纠正，再考虑 `guard` 常驻模式。

## 配置文件

与可执行文件同目录的 `edifier.json`（用 exe 路径定位，不依赖工作目录）：

```json
{
  "address": "AA:BB:CC:DD:EE:FF",
  "name": "EDIFIER BLE",
  "saved_at": "2026-11-09T10:21:20+08:00"
}
```

- 地址失效或配置文件损坏时会**自动重新扫描并覆盖修复**，不需要手动删文件。
- 想强制换设备：`edifier-control.exe status -discover`。

## 兼容性

| 型号 | 控制服务 UUID | 状态 |
| --- | --- | --- |
| W820NB 双金标版 | `48097901-1a48-11e9-ab14-d663bd873d93` | 已验证 |
| W820NB / W820NB Plus | `48093801-1a48-11e9-ab14-d663bd873d93` | 代码已兼容，未实测 |
| 其他漫步者 BLE 型号 | — | 若使用同族协议，可尝试 `discover` |

程序会先尝试双金标版的服务 UUID，失败再尝试普通版。

## 环境要求

- Windows 10 1809+ / Windows 11
- **电脑蓝牙适配器需开启**（仅需 BLE，不必把耳机配成音频设备；USB 音频与 BLE 控制互不干扰）
- 编译需要 Go 1.25+（依赖 `tinygo.org/x/bluetooth` v0.17.0）

## 构建

```bash
go build -o edifier-control.exe .
go test ./...
```

## 已知限制

- 固件不持久化音效模式，这是硬件行为，任何外部工具都只能"事后纠正"。
- 手机 App 占用 BLE 连接时电脑可能连不上，程序会退避重试。
- 耳机按键循环掩码掉电后是否保持，取决于具体固件版本，建议用 `fix` 后断电重开验证一次。

## 免责声明

非官方工具，与漫步者（Edifier）无任何关联。协议来自社区逆向分析，固件升级后可能失效。所有命令均为可回退的状态设置，但仍请自行承担使用风险。

## 致谢

- [estevao-tarifa/edifier-macos](https://github.com/estevao-tarifa/edifier-macos) —— 完整的协议文档与命令表
- [wh201906/mEDIFIER](https://github.com/wh201906/mEDIFIER) —— 原始的 App 抓包与协议分析
- [tinygo.org/x/bluetooth](https://github.com/tinygo-org/bluetooth) —— Windows BLE 后端

## License

MIT
