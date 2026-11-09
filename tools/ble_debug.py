"""调试：打印与耳机之间的原始 BLE 收发字节。"""
import asyncio
import sys

from bleak import BleakClient

from edifier import ADDR, CH_NOTIFY, CH_WRITE, build_frame

LOG = []


def on_notify(_c, data: bytearray):
    raw = bytes(data)
    LOG.append(raw)
    print(f"  <- {raw.hex(' ')}")


async def attempt(addr, use_response, payload):
    print(f"\n=== write_gatt_char(response={use_response}) frame={build_frame(payload).hex(' ')}")
    LOG.clear()
    async with BleakClient(addr, timeout=25) as client:
        await client.start_notify(CH_NOTIFY, on_notify)
        await asyncio.sleep(0.3)
        try:
            await client.write_gatt_char(CH_WRITE, build_frame(payload), response=use_response)
            print("  write ok")
        except Exception as e:
            print(f"  write failed: {type(e).__name__}: {e}")
        await asyncio.sleep(2.0)
        try:
            await client.stop_notify(CH_NOTIFY)
        except Exception:
            pass
    print(f"  received {len(LOG)} notification(s)")


async def main():
    addr = sys.argv[1] if len(sys.argv) > 1 else ADDR
    for use_response in (False, True):
        await attempt(addr, use_response, bytes([0xCC]))
    print("\nnow trying C1 01 (standard) with response=True")
    await attempt(addr, True, bytes([0xC1, 0x01]))


if __name__ == "__main__":
    asyncio.run(main())
