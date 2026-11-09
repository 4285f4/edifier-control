package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// feedStdin 把给定输入挂到 os.Stdin，返回还原函数。
func feedStdin(t *testing.T, input string) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	go func() {
		_, _ = w.WriteString(input)
		_ = w.Close()
	}()
	return func() {
		os.Stdin = old
		_ = r.Close()
	}
}

func TestChooseCandidateInteractive(t *testing.T) {
	cands := []candidate{
		{addr: "AA:AA:AA:AA:AA:01", name: "EDIFIER BLE", rssi: -50},
		{addr: "AA:AA:AA:AA:AA:02", name: "EDIFIER BLE", rssi: -70},
	}
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"1\n", "AA:AA:AA:AA:AA:01", false},
		{"2\n", "AA:AA:AA:AA:AA:02", false},
		{" 2 \n", "AA:AA:AA:AA:AA:02", false},
		{"3\n", "", true},
		{"0\n", "", true},
		{"abc\n", "", true},
		{"\n", "", true},
	}
	for _, tc := range cases {
		restore := feedStdin(t, tc.input)
		got, err := chooseCandidate(cands, true)
		restore()
		if tc.wantErr {
			if err == nil {
				t.Errorf("输入 %q 期望报错，实际得到 %s", tc.input, got.addr)
			}
			continue
		}
		if err != nil {
			t.Errorf("输入 %q 意外报错: %v", tc.input, err)
			continue
		}
		if got.addr != tc.want {
			t.Errorf("输入 %q 期望 %s，实际 %s", tc.input, tc.want, got.addr)
		}
	}
}

func TestChooseCandidateNonInteractive(t *testing.T) {
	cands := []candidate{
		{addr: "AA:AA:AA:AA:AA:01", rssi: -50},
		{addr: "AA:AA:AA:AA:AA:02", rssi: -70},
	}
	got, err := chooseCandidate(cands, false)
	if err != nil {
		t.Fatalf("非交互分支不应报错: %v", err)
	}
	if got.addr != cands[0].addr {
		t.Errorf("非交互应取信号最强者 %s，实际 %s", cands[0].addr, got.addr)
	}

	// 单设备且非交互：直接返回
	one, err := chooseCandidate(cands[:1], false)
	if err != nil || one.addr != cands[0].addr {
		t.Errorf("单设备应直接返回，实际 %v %v", one.addr, err)
	}
}

func TestParseArgs(t *testing.T) {
	cases := []struct {
		argv    []string
		action  string
		addr    string
		logPath string
		disc    bool
		wantErr bool
	}{
		{argv: []string{"status"}, action: "status"},
		{argv: []string{"status", "-addr", "AA:BB:CC:DD:EE:FF"}, action: "status", addr: "AA:BB:CC:DD:EE:FF"},
		{argv: []string{"-addr", "AA:BB:CC:DD:EE:FF", "status"}, action: "status", addr: "AA:BB:CC:DD:EE:FF"},
		{argv: []string{"status", "-addr=AA:BB:CC:DD:EE:FF"}, action: "status", addr: "AA:BB:CC:DD:EE:FF"},
		{argv: []string{"guard", "-log", "g.log"}, action: "guard", logPath: "g.log"},
		{argv: []string{"status", "-discover"}, action: "status", disc: true},
		{argv: []string{"status", "-addr"}, wantErr: true},
		{argv: []string{"status", "-bogus"}, wantErr: true},
		{argv: []string{"status", "extra"}, wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseArgs(tc.argv)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%v 期望报错", tc.argv)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v 意外报错: %v", tc.argv, err)
			continue
		}
		if got.action != tc.action || got.addr != tc.addr || got.logPath != tc.logPath || got.discover != tc.disc {
			t.Errorf("%v 解析结果不符: %+v", tc.argv, got)
		}
	}
}

func TestFramingRoundTrip(t *testing.T) {
	// 文档给出的一手实测样例：AA 02 C1 01 -> AA 02 C1 01 21 87
	got := buildFrame([]byte{0xC1, 0x01})
	want := "aa02c1012187"
	if strings.ToLower(hex.EncodeToString(got)) != want {
		t.Errorf("组帧错误: 期望 %s 实际 %x", want, got)
	}

	frames, rest := parseFrames([]byte{0xBB, 0x03, 0xCC, 0x01, 0x06, 0x21, 0xAA})
	if len(frames) != 1 || len(rest) != 0 {
		t.Fatalf("解析应得 1 帧且无残留，实际 %d 帧 残留%d", len(frames), len(rest))
	}
	if len(frames[0]) != 3 || frames[0][0] != 0xCC || frames[0][1] != 0x01 {
		t.Errorf("帧内容错误: %x", frames[0])
	}

	// 校验和错误的帧应被丢弃
	frames, _ = parseFrames([]byte{0xBB, 0x03, 0xCC, 0x01, 0x06, 0x00, 0x00})
	if len(frames) != 0 {
		t.Errorf("校验失败的帧不应被接受，实际 %d 帧", len(frames))
	}
}

func TestDescribeModeChinese(t *testing.T) {
	cases := []struct {
		mode byte
		want string
	}{
		{0x01, "标准"},
		{0x02, "降噪"},
		{0x03, "环境声"},
		{0x09, "0x09"}, // 未知值原样输出
	}
	for _, tc := range cases {
		if got := describeMode(tc.mode); got != tc.want {
			t.Errorf("describeMode(0x%02X) = %q，期望 %q", tc.mode, got, tc.want)
		}
	}
}

func TestDescribeMask(t *testing.T) {
	cases := []struct {
		mask byte
		want string
	}{
		{0x00, "无（按键无法切换）"},
		{0x01, "标准"},
		{0x06, "降噪 + 环境声"},      // 用户遇到的"标准模式丢失"状态
		{0x07, "标准 + 降噪 + 环境声"}, // 正常三档
	}
	for _, tc := range cases {
		if got := describeMask(tc.mask); got != tc.want {
			t.Errorf("describeMask(0x%02X) = %q，期望 %q", tc.mask, got, tc.want)
		}
	}
}
