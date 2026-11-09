"""扫描周边 BLE 设备，定位漫步者耳机的控制通道服务。"""
import asyncio
import os
import sys

from bleak import BleakScanner

TARGET_SVC = "48097901-1a48-11e9-ab14-d663bd873d93"
# 已知设备地址（可选，用于高亮）：可用环境变量 EDIFIER_MAC 覆盖
KNOWN_MAC = os.environ.get("EDIFIER_MAC", "AA:BB:CC:DD:EE:FF")


async def scan(seconds=12):
    print(f"scanning {seconds}s ...")
    found = await BleakScanner.discover(timeout=seconds, return_adv=True)
    hits = []
    for addr, (dev, adv) in found.items():
        name = dev.name or adv.local_name or ""
        uuids = [str(u).lower() for u in (adv.service_uuids or [])]
        interesting = (
            "edifier" in name.lower()
            or addr.upper() == KNOWN_MAC
            or TARGET_SVC in uuids
        )
        line = (f"{'*' if interesting else ' '} {addr}  rssi={adv.rssi:>4}  "
                f"name={name!r}  uuids={uuids}")
        if interesting:
            hits.append(line)
        print(line)
    print("\n--- matches ---")
    for h in hits:
        print(h)
    if not hits:
        print("(none)")


if __name__ == "__main__":
    sec = int(sys.argv[1]) if len(sys.argv) > 1 else 12
    asyncio.run(scan(sec))
