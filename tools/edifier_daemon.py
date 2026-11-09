"""漫步者 W820NB 双金标版 —— 模式守护进程。

用途：耳机固件不记忆音效模式，每次开机回落降噪（并可能丢掉"标准模式"这一档）。
本进程常驻后台，一旦检测到耳机 BLE 控制通道可用，就把状态校准为：
    - 按键循环掩码 = 0x07（标准 + 降噪 + 环境声 三档齐全）
    - 当前模式     = 0x01（标准）

用户之后手动按键切换不会被强行扳回；只有链路重建（耳机重新开机/重连）时才校准一次。

运行：pythonw edifier_daemon.py        （无窗口，日志写入 daemon.log）
"""
from __future__ import annotations

import asyncio
import logging
import os
import sys
from datetime import datetime

# 允许从任意工作目录启动（开机自启时 cwd 不是脚本目录）
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from bleak import BleakClient, BleakScanner

from edifier import (
    ADDR,
    CH_NOTIFY,
    CH_WRITE,
    MODE_NAMES,
    build_frame,
)

TARGET_NAME = "EDIFIER BLE"
MASK_ALL = 0x07          # bit0 标准 / bit1 降噪 / bit2 环境声
MODE_STANDARD = 0x01
HEARTBEAT = 30.0         # 秒，链路存活探测间隔
BACKOFF = [2, 4, 6, 10, 15]

LOG_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "daemon.log")
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)-7s %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
    handlers=[
        logging.FileHandler(LOG_PATH, encoding="utf-8"),
        logging.StreamHandler(sys.stdout),
    ],
)
log = logging.getLogger("edifier")


class EdifierGuard:
    def __init__(self, address: str = ADDR):
        self.address = address
        self.buf = bytearray()
        self.responses: list[bytes] = []
        self.client: BleakClient | None = None

    # ---------- 协议层 ----------
    def _on_notify(self, _char, data: bytearray):
        self.buf.extend(data)
        while len(self.buf) >= 2:
            if self.buf[0] not in (0xBB, 0xCC):
                self.buf.pop(0)
                continue
            length = self.buf[1]
            total = 2 + length + 2
            if len(self.buf) < total:
                return
            frame = bytes(self.buf[:total])
            del self.buf[:total]
            body = frame[2:2 + length]
            seed = (0x2019 + sum(frame[:-2])) & 0xFFFF
            if frame[-2:] == bytes([seed >> 8, seed & 0xFF]):
                self.responses.append(body)
            else:
                log.warning("校验失败，丢弃帧: %s", frame.hex(" "))

    async def ask(self, payload: bytes, timeout: float = 1.5) -> bytes | None:
        assert self.client
        self.responses.clear()
        frame = build_frame(payload)
        await self.client.write_gatt_char(CH_WRITE, frame, response=True)
        deadline = asyncio.get_running_loop().time() + timeout
        while not self.responses and asyncio.get_running_loop().time() < deadline:
            await asyncio.sleep(0.02)
        return self.responses[0] if self.responses else None

    # ---------- 连接层 ----------
    async def resolve_address(self) -> str | None:
        """按已知地址直连；失败则扫描按名称/UUID 匹配。"""
        try:
            dev = await BleakScanner.find_device_by_address(self.address, timeout=6)
            if dev:
                return dev.address
        except Exception:
            pass
        try:
            found = await BleakScanner.discover(timeout=8, return_adv=True)
        except Exception as e:
            log.debug("扫描失败: %s", e)
            return None
        for addr, (dev, adv) in found.items():
            name = (dev.name or adv.local_name or "").upper()
            uuids = " ".join(str(u).lower() for u in (adv.service_uuids or []))
            if TARGET_NAME.upper() in name or "48097901" in uuids or "00007900" in uuids:
                return addr
        return None

    async def calibrate(self):
        """把耳机校准到 标准模式 + 三档循环。"""
        assert self.client
        anc = await self.ask(bytes([0xCC]))
        mask = await self.ask(bytes([0xF0, 0x0A]))

        cur_mode = anc[1] if anc and len(anc) >= 3 else None
        cur_mask = mask[2] if mask and len(mask) >= 3 else None
        log.info("当前状态: 模式=%s 掩码=%s",
                 MODE_NAMES.get(cur_mode, cur_mode),
                 f"0x{cur_mask:02X}" if cur_mask is not None else "?")

        if cur_mask is not None and cur_mask != MASK_ALL:
            r = await self.ask(bytes([0xF1, 0x0A, MASK_ALL]))
            log.info("修正按键循环掩码 -> 0x%02X (应答 %s)",
                     MASK_ALL, r.hex(" ") if r else "无")

        if cur_mode != MODE_STANDARD:
            r = await self.ask(bytes([0xC1, MODE_STANDARD]))
            log.info("切换音效模式 -> 标准 (应答 %s)", r.hex(" ") if r else "无")
            verify = await self.ask(bytes([0xCC]))
            got = verify[1] if verify and len(verify) >= 3 else None
            if got == MODE_STANDARD:
                log.info("校准成功：已是标准模式")
            else:
                log.warning("校准未生效，回读模式=%s", got)
        else:
            log.info("无需校准：已是标准模式")

    async def session(self):
        self.buf.clear()
        self.responses.clear()
        self.client = BleakClient(self.address, timeout=25)
        await self.client.connect()
        await self.client.start_notify(CH_NOTIFY, self._on_notify)
        await asyncio.sleep(0.3)
        log.info("已连接 %s", self.address)
        await self.calibrate()
        while self.client.is_connected:
            await asyncio.sleep(HEARTBEAT)
            if not self.client.is_connected:
                break
            if await self.ask(bytes([0xCC]), timeout=3.0) is None:
                log.warning("心跳无响应，判定链路失效")
                break
        raise ConnectionError("link lost")

    async def run_forever(self):
        attempt = 0
        while True:
            try:
                if self.client and self.client.is_connected:
                    await self.session()
                else:
                    addr = await self.resolve_address()
                    if not addr:
                        raise ConnectionError("未发现耳机")
                    self.address = addr
                    await self.session()
                attempt = 0
            except asyncio.CancelledError:
                raise
            except Exception as e:
                delay = BACKOFF[min(attempt, len(BACKOFF) - 1)]
                log.info("等待耳机可用（%s），%ds 后重试", e, delay)
                attempt += 1
                await asyncio.sleep(delay)
            finally:
                try:
                    if self.client:
                        await self.client.disconnect()
                except Exception:
                    pass
                self.client = None


async def main():
    log.info("=" * 60)
    log.info("模式守护启动 %s", datetime.now().strftime("%Y-%m-%d %H:%M:%S"))
    guard = EdifierGuard(sys.argv[1] if len(sys.argv) > 1 else ADDR)
    await guard.run_forever()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        log.info("收到退出信号")
