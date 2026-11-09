"""探测耳机能力：固件版本、手势掩码、电量、名称等。"""
import asyncio

from edifier import ADDR, EdifierClient


async def main():
    async with EdifierClient(ADDR) as c:
        for label, payload in [
            ("ANC mode (CC)", bytes([0xCC])),
            ("gesture mask (F0 0A)", bytes([0xF0, 0x0A])),
            ("firmware (C6)", bytes([0xC6])),
            ("battery (D0)", bytes([0xD0])),
            ("name (C9)", bytes([0xC9])),
            ("sound effect (D5)", bytes([0xD5])),
            ("game mode (08)", bytes([0x08])),
        ]:
            res = await c.send(payload, timeout=1.5)
            if res:
                for r in res:
                    print(f"{label:24s} -> {r.hex(' ')}")
            else:
                print(f"{label:24s} -> (no reply / unsupported)")
            await asyncio.sleep(0.2)


if __name__ == "__main__":
    asyncio.run(main())
