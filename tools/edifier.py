"""漫步者 W820NB 双金标版 BLE 控制协议实现（逆向自公开协议文档）。

帧格式：
    host -> device : AA | len | payload[len] | crc_hi | crc_lo
    device -> host : BB | len | payload[len] | crc_hi | crc_lo   (状态/数值应答)
    device -> host : CC | len | payload[len] | crc_hi | crc_lo   (确认应答)

crc = (0x2019 + 帧内此前所有字节之和) & 0xFFFF，大端写出。
"""
from __future__ import annotations

import asyncio
import os
from dataclasses import dataclass, field

from bleak import BleakClient

# 耳机 BLE 地址：可用环境变量 EDIFIER_ADDR 覆盖，默认值为占位符
ADDR = os.environ.get("EDIFIER_ADDR", "AA:BB:CC:DD:EE:FF")
SVC_CTRL = "48097901-1a48-11e9-ab14-d663bd873d93"
CH_NOTIFY = "48090001-1a48-11e9-ab14-d663bd873d93"
CH_WRITE = "48090002-1a48-11e9-ab14-d663bd873d93"

HEADER_HOST = 0xAA
HEADER_STATE = 0xBB
HEADER_ACK = 0xCC
CRC_SEED = 0x2019
CHUNK = 20
CHUNK_GAP = 0.05

MODE_NAMES = {0x01: "standard", 0x02: "anc", 0x03: "ambient"}


def checksum(frame_without_crc: bytes) -> bytes:
    total = (CRC_SEED + sum(frame_without_crc)) & 0xFFFF
    return bytes([total >> 8, total & 0xFF])


def build_frame(payload: bytes) -> bytes:
    head = bytes([HEADER_HOST, len(payload)]) + payload
    return head + checksum(head)


def verify_frame(frame: bytes) -> bool:
    return len(frame) >= 4 and frame[-2:] == checksum(frame[:-2])


@dataclass
class EdifierClient:
    address: str = ADDR
    responses: list[bytes] = field(default_factory=list)
    _client: BleakClient | None = None
    _buf: bytearray = field(default_factory=bytearray)

    def _on_notify(self, _char, data: bytearray):
        self._buf.extend(data)
        while len(self._buf) >= 2:
            head = self._buf[0]
            if head not in (HEADER_STATE, HEADER_ACK):
                self._buf.pop(0)
                continue
            length = self._buf[1]
            total = 2 + length + 2
            if len(self._buf) < total:
                return
            frame = bytes(self._buf[:total])
            del self._buf[:total]
            if verify_frame(frame):
                self.responses.append(frame[2:2 + length])
            else:
                self.responses.append(b"\x00CRC_FAIL" + frame[2:2 + length])

    async def __aenter__(self):
        self._client = BleakClient(self.address, timeout=25)
        await self._client.connect()
        await self._client.start_notify(CH_NOTIFY, self._on_notify)
        await asyncio.sleep(0.2)
        return self

    async def __aexit__(self, *exc):
        try:
            if self._client and self._client.is_connected:
                await self._client.stop_notify(CH_NOTIFY)
                await self._client.disconnect()
        except Exception:
            pass

    async def send(self, payload: bytes, expect: int | None = 1, timeout: float = 2.0) -> list[bytes]:
        assert self._client
        frame = build_frame(payload)
        # 必须先清空缓冲再写入，否则应答可能在写入返回前就落地并被误清
        self.responses.clear()
        if not expect:
            for i in range(0, len(frame), CHUNK):
                await self._client.write_gatt_char(CH_WRITE, frame[i:i + CHUNK], response=True)
                await asyncio.sleep(CHUNK_GAP)
            return []
        for i in range(0, len(frame), CHUNK):
            await self._client.write_gatt_char(CH_WRITE, frame[i:i + CHUNK], response=True)
            await asyncio.sleep(CHUNK_GAP)
        deadline = asyncio.get_event_loop().time() + timeout
        while len(self.responses) < expect and asyncio.get_event_loop().time() < deadline:
            await asyncio.sleep(0.02)
        return list(self.responses)

    async def query_anc(self) -> tuple[int | None, int | None]:
        """CC -> BB 03 CC <mode> <volbyte>"""
        res = await self.send(bytes([0xCC]))
        for r in res:
            if len(r) >= 3 and r[0] == 0xCC:
                return r[1], r[2]
        return None, None

    async def set_anc(self, mode: int, vol_byte: int | None = None) -> bytes | None:
        """C1 <mode> [volbyte] -> BB 03 C1 <mode> <volbyte>"""
        payload = bytes([0xC1, mode]) if vol_byte is None else bytes([0xC1, mode, vol_byte])
        res = await self.send(payload)
        return res[0] if res else None


def describe(mode: int | None, vol: int | None) -> str:
    if mode is None:
        return "unknown"
    name = MODE_NAMES.get(mode, f"0x{mode:02X}")
    extra = ""
    if mode == 0x03 and vol is not None:
        extra = f" (ambient level {vol - 6:+d})"
    return f"{name}{extra}"


async def _status(addr: str):
    async with EdifierClient(addr) as c:
        mode, vol = await c.query_anc()
        print(f"raw mode=0x{mode:02X} vol=0x{vol:02X}" if mode is not None
              else "no response")
        print("ANC state:", describe(mode, vol))


async def _set(addr: str, target: str):
    mode = {"standard": 0x01, "anc": 0x02, "ambient": 0x03}[target]
    async with EdifierClient(addr) as c:
        before, _ = await c.query_anc()
        print("before:", describe(before, None))
        r = await c.set_anc(mode)
        print("set reply:", r.hex(" ") if r else None)
        await asyncio.sleep(0.3)
        after, vol = await c.query_anc()
        print("after :", describe(after, vol))
        print("RESULT:", "OK" if after == mode else "MISMATCH")


if __name__ == "__main__":
    import sys

    cmd = sys.argv[1] if len(sys.argv) > 1 else "status"
    target_addr = sys.argv[2] if len(sys.argv) > 2 else ADDR
    if cmd == "status":
        asyncio.run(_status(target_addr))
    elif cmd in ("standard", "anc", "ambient"):
        asyncio.run(_set(target_addr, cmd))
    else:
        print("usage: edifier.py status | standard | anc | ambient [address]")
