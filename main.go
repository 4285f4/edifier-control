// Edifier W820NB 双金标版 音效模式控制工具（Windows BLE）
//
// 背景：该耳机固件不记忆音效模式，每次开机回落降噪，且"标准模式"会从按键
// 循环中丢失。USB 侧的 HID 接口只有输入报告（OutputReport=0），无法反向下发
// 命令；因此改走 BLE 控制通道（与音频链路独立，不影响 USB/蓝牙音频）。
//
// 用法：
//
//	edifier-control.exe status            查看当前模式
//	edifier-control.exe standard          切到标准模式（关闭降噪/环境声）
//	edifier-control.exe anc               切到降噪模式
//	edifier-control.exe ambient           切到环境声模式
//	edifier-control.exe fix               把按键循环修复为三档（标准/降噪/环境声）
//	edifier-control.exe guard             常驻：连上耳机即校准为「三档循环 + 标准模式」
//	edifier-control.exe guard -log file  同上，日志写入文件
//	edifier-control.exe discover          扫描并打印当前可用的漫步者设备
//
// 设备地址不写死：首次运行会自动扫描，找到一台就直接连接，多台则列出让你选，
// 连接成功后把地址写入程序同目录的 edifier.json，后续运行直接复用。
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

const (
	// 控制服务：双金标版 48097901，普通版 48093801，两者都尝试
	svcUUIDStr    = "48097901-1a48-11e9-ab14-d663bd873d93"
	svcUUIDStrAlt = "48093801-1a48-11e9-ab14-d663bd873d93"
	chNotifyStr   = "48090001-1a48-11e9-ab14-d663bd873d93"
	chWriteStr    = "48090002-1a48-11e9-ab14-d663bd873d93"

	configFileName = "edifier.json"
	targetName     = "EDIFIER" // 广播名匹配关键字，覆盖 W820NB 各版本

	cmdQueryANC    = 0xCC
	cmdSetANC      = 0xC1
	cmdQueryMask   = 0xF0
	cmdSetMask     = 0xF1
	maskAll        = 0x07 // bit0 标准 / bit1 降噪 / bit2 环境声
	modeStandard   = 0x01
	headerHost     = 0xAA
	headerState    = 0xBB
	headerAck      = 0xCC
	crcSeed        = 0x2019
	heartbeatTick  = 30 * time.Second
	connectTimeout = 25 * time.Second
)

var (
	errNoResponse = errors.New("耳机无响应")
	backoff       = []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second, 10 * time.Second, 15 * time.Second}
	modeNames     = map[byte]string{0x01: "标准", 0x02: "降噪", 0x03: "环境声"}
	svcUUID       = mustUUID(svcUUIDStr)
	svcUUIDAlt    = mustUUID(svcUUIDStrAlt)
)

// ---------- 协议层 ----------

// buildFrame 按 AA|len|payload|sum 组帧，sum = (0x2019 + 各字节之和) & 0xFFFF，
// 大端写入。
func buildFrame(payload []byte) []byte {
	f := make([]byte, 0, len(payload)+4)
	f = append(f, headerHost, byte(len(payload)))
	f = append(f, payload...)
	sum := crcSeed
	for _, b := range f {
		sum += int(b)
	}
	return append(f, byte((sum>>8)&0xFF), byte(sum&0xFF))
}

// parseFrames 从字节流中切出完整帧，丢弃无法对齐的字节。
func parseFrames(buf []byte) (frames [][]byte, rest []byte) {
	for len(buf) >= 2 {
		if buf[0] != headerState && buf[0] != headerAck {
			buf = buf[1:]
			continue
		}
		length := int(buf[1])
		total := 2 + length + 2
		if len(buf) < total {
			break
		}
		cand := buf[:total]
		sum := crcSeed
		for _, b := range cand[:total-2] {
			sum += int(b)
		}
		if byte((sum>>8)&0xFF) == cand[total-2] && byte(sum&0xFF) == cand[total-1] {
			frames = append(frames, cand[2:total-2])
			buf = buf[total:]
		} else {
			buf = buf[1:]
		}
	}
	return frames, buf
}

// Session 维护一条已建立的 BLE 控制链路。
type Session struct {
	dev   bluetooth.Device
	write bluetooth.DeviceCharacteristic

	mu   sync.Mutex
	buf  []byte
	resp chan []byte
}

func (s *Session) onNotify(data []byte) {
	s.mu.Lock()
	s.buf = append(s.buf, data...)
	frames, rest := parseFrames(s.buf)
	s.buf = rest
	s.mu.Unlock()
	for _, f := range frames {
		select {
		case s.resp <- f:
		default: // 丢弃无人消费的旧应答，避免阻塞回调
		}
	}
}

// ask 发送一条命令并等待首个应答 payload。
// 固件对"无变化的设置"偶尔不回包，这里重试一次以吸收偶发丢包。
func (s *Session) ask(payload []byte, timeout time.Duration) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		for len(s.resp) > 0 { // 清掉残留应答
			<-s.resp
		}
		if _, err := s.write.Write(buildFrame(payload)); err != nil {
			lastErr = fmt.Errorf("写入失败: %w", err)
		} else {
			select {
			case r := <-s.resp:
				return r, nil
			case <-time.After(timeout):
				lastErr = errNoResponse
			}
		}
		if attempt == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return nil, lastErr
}

func (s *Session) queryANC() (mode byte, vol byte, err error) {
	r, err := s.ask([]byte{cmdQueryANC}, 3*time.Second)
	if err != nil {
		return 0, 0, err
	}
	if len(r) < 3 || r[0] != cmdQueryANC {
		return 0, 0, fmt.Errorf("查询应答异常: %s", hex.EncodeToString(r))
	}
	return r[1], r[2], nil
}

func (s *Session) setANC(mode byte) (byte, error) {
	r, err := s.ask([]byte{cmdSetANC, mode}, 3*time.Second)
	if err != nil {
		return 0, err
	}
	if len(r) < 2 || r[0] != cmdSetANC {
		return 0, fmt.Errorf("设置应答异常: %s", hex.EncodeToString(r))
	}
	return r[1], nil
}

func (s *Session) queryMask() (byte, error) {
	r, err := s.ask([]byte{cmdQueryMask, 0x0A}, 3*time.Second)
	if err != nil {
		return 0, err
	}
	if len(r) < 3 || r[0] != cmdQueryMask {
		return 0, fmt.Errorf("掩码应答异常: %s", hex.EncodeToString(r))
	}
	return r[2], nil
}

func (s *Session) setMask(v byte) (byte, error) {
	r, err := s.ask([]byte{cmdSetMask, 0x0A, v}, 3*time.Second)
	if err != nil {
		return 0, err
	}
	if len(r) < 3 || r[0] != cmdSetMask {
		return 0, fmt.Errorf("掩码设置应答异常: %s", hex.EncodeToString(r))
	}
	return r[2], nil
}

// calibrate 把耳机校准为「三档循环 + 标准模式」，仅在偏离时下发。
func (s *Session) calibrate(verbose bool) error {
	mode, _, err := s.queryANC()
	if err != nil {
		return fmt.Errorf("读取模式失败: %w", err)
	}
	mask, err := s.queryMask()
	if err != nil {
		if verbose {
			log.Printf("读取循环掩码失败（可忽略）: %v", err)
		}
	} else if mask != maskAll {
		if got, err := s.setMask(maskAll); err != nil {
			log.Printf("修正循环掩码失败: %v", err)
		} else {
			log.Printf("按键循环 %s -> %s（三档已恢复）", describeMask(mask), describeMask(got))
		}
	}

	if mode == modeStandard {
		log.Printf("当前已是标准模式，无需调整")
		return nil
	}
	if got, err := s.setANC(modeStandard); err != nil {
		return fmt.Errorf("切换标准模式失败: %w", err)
	} else if got != modeStandard {
		return fmt.Errorf("切换未生效，回读模式 0x%02X", got)
	}
	log.Printf("已从 %s 切换到 %s", describeMode(mode), describeMode(modeStandard))
	return nil
}

func describeMode(m byte) string {
	if n, ok := modeNames[m]; ok {
		return n
	}
	return fmt.Sprintf("0x%02X", m)
}

// describeMask 把按键循环掩码翻译成可读的中文档位组合：
// bit0 标准 / bit1 降噪 / bit2 环境声。
func describeMask(mask byte) string {
	var parts []string
	if mask&0x01 != 0 {
		parts = append(parts, "标准")
	}
	if mask&0x02 != 0 {
		parts = append(parts, "降噪")
	}
	if mask&0x04 != 0 {
		parts = append(parts, "环境声")
	}
	if len(parts) == 0 {
		return "无（按键无法切换）"
	}
	return strings.Join(parts, " + ")
}

// ---------- 连接层 ----------

var adapter = bluetooth.DefaultAdapter

func mustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic("非法 UUID: " + s)
	}
	return u
}

// open 连接耳机并定位控制特征。
func open(addrStr string) (*Session, error) {
	mac, err := bluetooth.ParseMAC(addrStr)
	if err != nil {
		return nil, fmt.Errorf("非法 MAC %q: %w", addrStr, err)
	}
	address := bluetooth.Address{MACAddress: bluetooth.NewMACAddress(mac, false)}

	dev, err := adapter.Connect(address, bluetooth.ConnectionParams{
		ConnectionTimeout: bluetooth.NewDuration(12 * time.Second),
	})
	if err != nil {
		return nil, fmt.Errorf("连接失败: %w", err)
	}
	var svcs []bluetooth.DeviceService
	for _, u := range []bluetooth.UUID{svcUUID, svcUUIDAlt} {
		svcs, err = dev.DiscoverServices([]bluetooth.UUID{u})
		if err == nil && len(svcs) > 0 {
			break
		}
	}
	if err != nil {
		dev.Disconnect()
		return nil, fmt.Errorf("发现服务失败: %w", err)
	}
	if len(svcs) == 0 {
		dev.Disconnect()
		return nil, errors.New("未找到控制服务（型号可能不受支持）")
	}
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{
		mustUUID(chNotifyStr), mustUUID(chWriteStr),
	})
	if err != nil {
		dev.Disconnect()
		return nil, fmt.Errorf("发现特征失败: %w", err)
	}
	var notifyCh, writeCh bluetooth.DeviceCharacteristic
	for _, c := range chars {
		switch strings.ToLower(c.UUID().String()) {
		case chNotifyStr:
			notifyCh = c
		case chWriteStr:
			writeCh = c
		}
	}
	if writeCh.UUID().String() == "00000000-0000-0000-0000-000000000000" {
		dev.Disconnect()
		return nil, errors.New("未找到写入特征")
	}

	s := &Session{dev: dev, write: writeCh, resp: make(chan []byte, 8)}
	if err := notifyCh.EnableNotifications(s.onNotify); err != nil {
		dev.Disconnect()
		return nil, fmt.Errorf("订阅通知失败: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	return s, nil
}

// candidate 是一个扫描到的候选设备。
type candidate struct {
	addr string
	name string
	rssi int16
}

// scanAll 扫描 timeout 时长，收集所有匹配的漫步者设备，按信号强度降序返回。
//
// 注意：Windows 上 Scan 是阻塞调用，必须由 StopScan 结束；因此用定时器强制停止，
// 并用 sync.Once 保证只停一次（重复调用会返回 errNotScanning）。
func scanAll(timeout time.Duration) ([]candidate, error) {
	var (
		mu   sync.Mutex
		seen = make(map[string]int)
		out  []candidate
		once sync.Once
	)
	stop := func() { once.Do(func() { _ = adapter.StopScan() }) }
	timer := time.AfterFunc(timeout, stop)
	defer timer.Stop()

	err := adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		name := r.LocalName()
		upper := strings.ToUpper(name)
		matched := strings.Contains(upper, targetName) ||
			r.HasServiceUUID(svcUUID) || r.HasServiceUUID(svcUUIDAlt)
		if !matched {
			return
		}
		addr := r.Address.String()

		mu.Lock()
		defer mu.Unlock()
		if i, ok := seen[addr]; ok {
			if r.RSSI > out[i].rssi {
				out[i].rssi = r.RSSI
			}
			if out[i].name == "" {
				out[i].name = name
			}
			return
		}
		seen[addr] = len(out)
		out = append(out, candidate{addr: addr, name: name, rssi: r.RSSI})
	})

	mu.Lock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].rssi > out[j].rssi })
	mu.Unlock()

	if len(out) == 0 && err != nil {
		return nil, fmt.Errorf("扫描失败: %w", err)
	}
	return out, nil
}

// discover 返回信号最强的匹配设备地址。
func discover(timeout time.Duration) (string, error) {
	cands, err := scanAll(timeout)
	if err != nil {
		return "", err
	}
	if len(cands) == 0 {
		return "", errors.New("未发现漫步者设备（确认耳机已开机、蓝牙已开启）")
	}
	return cands[0].addr, nil
}

// ---------- 配置文件 ----------

// config 是首次连接成功后落盘的设备信息。
type config struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	SavedAt string `json:"saved_at"`
}

// configPath 把配置放在可执行文件同目录，这样从任意工作目录启动都能复用
// （guard 作为开机自启时 cwd 往往不是 exe 所在目录）。
func configPath() string {
	exe, err := os.Executable()
	if err != nil {
		return configFileName
	}
	return filepath.Join(filepath.Dir(exe), configFileName)
}

func loadConfig(path string) (*config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("配置文件格式错误: %w", err)
	}
	if strings.TrimSpace(c.Address) == "" {
		return nil, errors.New("配置文件缺少 address")
	}
	return &c, nil
}

func saveConfig(path string, cand candidate) error {
	b, err := json.MarshalIndent(config{
		Address: cand.addr,
		Name:    cand.name,
		SavedAt: time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ---------- 连接 ----------

// connector 按「显式地址 → 配置文件 → 扫描引导」的优先级定位耳机。
type connector struct {
	addrOverride string // -addr 显式指定，严格使用，不自动回退
	force        bool   // -discover 强制重新扫描
	cfgPath      string
}

func newConnector(addrFlag string, forceDiscover bool) *connector {
	return &connector{
		addrOverride: strings.TrimSpace(addrFlag),
		force:        forceDiscover,
		cfgPath:      configPath(),
	}
}

func (c *connector) dial() (*Session, error) {
	if c.addrOverride != "" {
		return open(c.addrOverride)
	}
	if !c.force {
		cfg, err := loadConfig(c.cfgPath)
		if err == nil {
			s, err := open(cfg.Address)
			if err == nil {
				return s, nil
			}
			log.Printf("配置中的 %s 连接失败（%v），重新扫描", cfg.Address, err)
		} else if !os.IsNotExist(err) {
			log.Printf("读取配置失败（%v），重新扫描", err)
		}
	}
	return c.discoverAndSave()
}

// discoverAndSave 用于首次配置或地址失效：扫描 → 选择 → 连接 → 落盘。
func (c *connector) discoverAndSave() (*Session, error) {
	log.Printf("正在扫描漫步者设备（最多 8 秒）...")
	cands, err := scanAll(8 * time.Second)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, errors.New("未发现漫步者设备（确认耳机已开机、未被其他设备占用）")
	}

	pick, err := chooseCandidate(cands, isInteractive())
	if err != nil {
		return nil, err
	}
	s, err := open(pick.addr)
	if err != nil {
		return nil, err
	}
	if err := saveConfig(c.cfgPath, pick); err != nil {
		log.Printf("配置写入失败: %v", err)
	} else {
		log.Printf("已记录设备 %s 到 %s", pick.addr, c.cfgPath)
	}
	return s, nil
}

// chooseCandidate 让用户在多个候选中挑选；非交互环境（如开机自启）自动取信号最强的。
// interactive 由调用方传入，便于在无终端环境下测试。
func chooseCandidate(cands []candidate, interactive bool) (candidate, error) {
	if len(cands) == 1 {
		log.Printf("发现设备 %s%s", cands[0].addr, nameSuffix(cands[0].name))
		return cands[0], nil
	}
	if !interactive {
		log.Printf("发现 %d 个设备，非交互环境自动选择信号最强的 %s", len(cands), cands[0].addr)
		return cands[0], nil
	}

	fmt.Printf("\n发现 %d 个漫步者设备：\n", len(cands))
	for i, c := range cands {
		fmt.Printf("  %d) %s  %-16s %d dBm\n", i+1, c.addr, c.name, c.rssi)
	}
	fmt.Printf("请选择要连接的设备 [1-%d]: ", len(cands))

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return candidate{}, fmt.Errorf("读取选择失败: %w", err)
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(line))
	if convErr != nil || n < 1 || n > len(cands) {
		return candidate{}, fmt.Errorf("无效选择 %q", strings.TrimSpace(line))
	}
	return cands[n-1], nil
}

func nameSuffix(name string) string {
	if name == "" {
		return ""
	}
	return "（" + name + "）"
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ---------- 命令 ----------

func runOnce(addrStr string, force bool, action string) int {
	if err := adapter.Enable(); err != nil {
		log.Printf("蓝牙适配器不可用: %v", err)
		return 2
	}
	conn := newConnector(addrStr, force)
	log.Printf("连接耳机 ...")
	s, err := conn.dial()
	if err != nil {
		log.Printf("%v", err)
		return 2
	}
	defer s.dev.Disconnect()

	switch action {
	case "status":
		mode, vol, err := s.queryANC()
		if err != nil {
			log.Printf("查询失败: %v", err)
			return 1
		}
		mask, err := s.queryMask()
		maskStr := "读取失败"
		if err == nil {
			maskStr = fmt.Sprintf("%s (0x%02X)", describeMask(mask), mask)
		}
		fmt.Printf("模式: %s (0x%02X)  环境声音量: %+d  按键循环: %s\n",
			describeMode(mode), mode, int(vol)-6, maskStr)
	case "standard", "anc", "ambient":
		want := map[string]byte{"standard": 0x01, "anc": 0x02, "ambient": 0x03}[action]
		before, _, err := s.queryANC()
		if err != nil {
			log.Printf("读取当前模式失败: %v", err)
			return 1
		}
		log.Printf("当前: %s", describeMode(before))
		if before == want {
			log.Printf("已是%s模式，无需切换", describeMode(want))
			return 0
		}
		got, err := s.setANC(want)
		if err != nil {
			log.Printf("%v", err)
			return 1
		}
		if got != want {
			log.Printf("设置未生效（回读 %s）", describeMode(got))
			return 1
		}
		log.Printf("已切换到%s", describeMode(got))
	case "fix":
		mask, err := s.queryMask()
		if err != nil {
			log.Printf("读取按键循环失败: %v", err)
			return 1
		}
		log.Printf("当前按键循环: %s (0x%02X)", describeMask(mask), mask)
		if mask == maskAll {
			log.Printf("三档已齐全，无需修复")
			return 0
		}
		got, err := s.setMask(maskAll)
		if err != nil {
			log.Printf("修复失败: %v", err)
			return 1
		}
		if got != maskAll {
			log.Printf("修复未生效（回读 0x%02X）", got)
			return 1
		}
		log.Printf("已恢复三档循环: %s (0x%02X)", describeMask(got), got)
	}
	return 0
}

// runGuard 常驻：连上即校准，断线按退避重连，定期心跳。
func runGuard(addrStr string, force bool) int {
	if err := adapter.Enable(); err != nil {
		log.Printf("蓝牙适配器不可用: %v", err)
		return 2
	}
	conn := newConnector(addrStr, force)
	if conn.addrOverride != "" {
		log.Printf("守护启动（指定地址 %s）", conn.addrOverride)
	} else {
		log.Printf("守护启动（配置 %s）", conn.cfgPath)
	}
	attempt := 0
	for {
		s, err := conn.dial()
		if err != nil {
			d := backoff[min(attempt, len(backoff)-1)]
			log.Printf("%v；%s 后重试", err, d)
			attempt++
			time.Sleep(d)
			continue
		}
		attempt = 0
		log.Printf("已连接")
		if err := s.calibrate(true); err != nil {
			log.Printf("校准失败: %v", err)
		}
		// 心跳：无应答即认为链路失效；顺带每 5 分钟巡检一次按键循环，
		// 防止在线期间被其他客户端（如手机 App）改成两档。
		ticks := 0
		for {
			time.Sleep(heartbeatTick)
			if _, _, err := s.queryANC(); err != nil {
				log.Printf("心跳失败(%v)，重新连接", err)
				break
			}
			ticks++
			if ticks%10 != 0 {
				continue
			}
			if mask, err := s.queryMask(); err == nil && mask != maskAll {
				if got, err := s.setMask(maskAll); err != nil {
					log.Printf("按键循环巡检：发现 %s，修复失败: %v", describeMask(mask), err)
				} else {
					log.Printf("按键循环巡检：发现 %s，已恢复为 %s",
						describeMask(mask), describeMask(got))
				}
			}
		}
		s.dev.Disconnect()
		time.Sleep(2 * time.Second)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func usage() {
	fmt.Fprintf(os.Stderr, `用法: edifier-control <命令> [选项]

命令:
  status     查看当前音效模式与按键循环掩码
  standard   切换到标准模式（关闭降噪/环境声）
  anc        切换到降噪模式
  ambient    切换到环境声模式
  fix        修复按键循环为三档（标准 / 降噪 / 环境声 都能按键切到）
  guard      常驻守护：连上耳机即校准为「三档循环 + 标准模式」
  discover   扫描并打印耳机的 BLE 地址（换耳机或地址失效时用）

选项:
  -addr MAC    直接指定耳机 BLE 地址（跳过配置文件，仅本次生效）
  -log PATH    guard 模式下把日志写入文件
  -discover    忽略配置文件，重新扫描并选择设备

首次运行会自动扫描漫步者设备，并把结果保存到程序同目录的 edifier.json：
  · 只找到一台：直接连接并记住
  · 找到多台：列出来，输入序号选择
  · 之后每次运行直接复用配置，无需再扫描

选项可写在命令前或命令后，例如：
  edifier-control status
  edifier-control status -discover
  edifier-control -addr AA:BB:CC:DD:EE:FF status
`)
}

// options 是解析后的命令行参数。
type options struct {
	action   string
	addr     string
	logPath  string
	discover bool
}

// parseArgs 手写解析，兼容「命令 + 选项」任意顺序。
// 标准 flag 包遇到首个非选项参数即停止解析，会把 `status -addr X` 里的 -addr 丢掉。
func parseArgs(argv []string) (options, error) {
	o := options{}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-addr" || a == "--addr":
			i++
			if i >= len(argv) {
				return o, errors.New("-addr 缺少取值")
			}
			o.addr = argv[i]
		case strings.HasPrefix(a, "-addr=") || strings.HasPrefix(a, "--addr="):
			o.addr = a[strings.Index(a, "=")+1:]
		case a == "-log" || a == "--log":
			i++
			if i >= len(argv) {
				return o, errors.New("-log 缺少取值")
			}
			o.logPath = argv[i]
		case strings.HasPrefix(a, "-log=") || strings.HasPrefix(a, "--log="):
			o.logPath = a[strings.Index(a, "=")+1:]
		case a == "-discover" || a == "--discover":
			o.discover = true
		case a == "-h" || a == "--help":
			usage()
			os.Exit(0)
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("未知选项 %q", a)
		default:
			if o.action != "" {
				return o, fmt.Errorf("多余参数 %q", a)
			}
			o.action = a
		}
	}
	return o, nil
}

func main() {
	opt, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n", err)
		usage()
		os.Exit(2)
	}
	if opt.action == "" {
		usage()
		os.Exit(2)
	}

	if opt.logPath != "" {
		f, err := os.OpenFile(opt.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法打开日志文件: %v\n", err)
			os.Exit(2)
		}
		defer f.Close()
		log.SetOutput(io.MultiWriter(f))
	}
	log.SetFlags(log.LstdFlags)

	switch opt.action {
	case "status", "standard", "anc", "ambient", "fix":
		os.Exit(runOnce(opt.addr, opt.discover, opt.action))
	case "guard":
		os.Exit(runGuard(opt.addr, opt.discover))
	case "discover":
		if err := adapter.Enable(); err != nil {
			log.Printf("蓝牙适配器不可用: %v", err)
			os.Exit(2)
		}
		addr, err := discover(10 * time.Second)
		if err != nil {
			log.Printf("%v", err)
			os.Exit(1)
		}
		fmt.Println(addr)
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", opt.action)
		usage()
		os.Exit(2)
	}
}
