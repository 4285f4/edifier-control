"""连接漫步者耳机 BLE 控制通道，枚举 GATT 服务与特征。"""
import asyncio
import os
import sys

from bleak import BleakClient

# 可用环境变量 EDIFIER_ADDR 覆盖，或直接作为第一个命令行参数传入
ADDR = os.environ.get("EDIFIER_ADDR", "AA:BB:CC:DD:EE:FF")


async def main():
    addr = sys.argv[1] if len(sys.argv) > 1 else ADDR
    print(f"connecting {addr} ...")
    async with BleakClient(addr, timeout=25) as client:
        print("connected:", client.is_connected)
        for svc in client.services:
            print("=" * 70)
            print(f"SERVICE {svc.uuid}  ({svc.description})")
            for ch in svc.characteristics:
                print(f"   CHAR {ch.uuid}  props={','.join(ch.properties)}  "
                      f"handle={ch.handle}")
                if ch.descriptors:
                    for d in ch.descriptors:
                        print(f"        DESC {d.uuid} handle={d.handle}")


if __name__ == "__main__":
    asyncio.run(main())
