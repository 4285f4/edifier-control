"""枚举本机 HID 设备并 dump 报告描述符（用于识别厂商自定义控制接口）。

仅依赖 ctypes + 系统 hid.dll / setupapi.dll，无需安装第三方库。
"""
import ctypes
from ctypes import wintypes

setupapi = ctypes.WinDLL("setupapi", use_last_error=True)
hid = ctypes.WinDLL("hid", use_last_error=True)
kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)

DIGCF_PRESENT = 0x02
DIGCF_DEVICEINTERFACE = 0x10
GENERIC_READ = 0x80000000
GENERIC_WRITE = 0x40000000
FILE_SHARE_READ = 0x01
FILE_SHARE_WRITE = 0x02
OPEN_EXISTING = 3
INVALID_HANDLE_VALUE = ctypes.c_void_p(-1).value


class GUID(ctypes.Structure):
    _fields_ = [
        ("Data1", wintypes.DWORD),
        ("Data2", wintypes.WORD),
        ("Data3", wintypes.WORD),
        ("Data4", ctypes.c_ubyte * 8),
    ]


class SP_DEVINFO_DATA(ctypes.Structure):
    _fields_ = [
        ("cbSize", wintypes.DWORD),
        ("ClassGuid", GUID),
        ("DevInst", wintypes.DWORD),
        ("Reserved", ctypes.c_void_p),
    ]


class SP_DEVICE_INTERFACE_DATA(ctypes.Structure):
    _fields_ = [
        ("cbSize", wintypes.DWORD),
        ("InterfaceClassGuid", GUID),
        ("Flags", wintypes.DWORD),
        ("Reserved", ctypes.c_void_p),
    ]


class SP_DEVICE_INTERFACE_DETAIL_DATA_W(ctypes.Structure):
    _fields_ = [
        ("cbSize", wintypes.DWORD),
        ("DevicePath", wintypes.WCHAR * 1),
    ]


class HIDD_ATTRIBUTES(ctypes.Structure):
    _fields_ = [
        ("Size", wintypes.ULONG),
        ("VendorID", wintypes.USHORT),
        ("ProductID", wintypes.USHORT),
        ("VersionNumber", wintypes.USHORT),
    ]


class HIDP_CAPS(ctypes.Structure):
    _fields_ = [
        ("Usage", wintypes.USHORT),
        ("UsagePage", wintypes.USHORT),
        ("InputReportByteLength", wintypes.USHORT),
        ("OutputReportByteLength", wintypes.USHORT),
        ("FeatureReportByteLength", wintypes.USHORT),
        ("Reserved", wintypes.USHORT * 17),
        ("NumberLinkCollectionNodes", wintypes.USHORT),
        ("NumberInputButtonCaps", wintypes.USHORT),
        ("NumberInputValueCaps", wintypes.USHORT),
        ("NumberInputDataIndices", wintypes.USHORT),
        ("NumberOutputButtonCaps", wintypes.USHORT),
        ("NumberOutputValueCaps", wintypes.USHORT),
        ("NumberOutputDataIndices", wintypes.USHORT),
        ("NumberFeatureButtonCaps", wintypes.USHORT),
        ("NumberFeatureValueCaps", wintypes.USHORT),
        ("NumberFeatureDataIndices", wintypes.USHORT),
    ]


setupapi.SetupDiGetClassDevsW.restype = wintypes.HANDLE
setupapi.SetupDiGetClassDevsW.argtypes = [ctypes.POINTER(GUID), wintypes.LPCWSTR,
                                        wintypes.HWND, wintypes.DWORD]
setupapi.SetupDiEnumDeviceInterfaces.restype = wintypes.BOOL
setupapi.SetupDiEnumDeviceInterfaces.argtypes = [wintypes.HANDLE, ctypes.POINTER(SP_DEVINFO_DATA),
                                                ctypes.POINTER(GUID), wintypes.DWORD,
                                                ctypes.POINTER(SP_DEVICE_INTERFACE_DATA)]
setupapi.SetupDiGetDeviceInterfaceDetailW.restype = wintypes.BOOL
setupapi.SetupDiGetDeviceInterfaceDetailW.argtypes = [
    wintypes.HANDLE, ctypes.POINTER(SP_DEVICE_INTERFACE_DATA),
    ctypes.POINTER(SP_DEVICE_INTERFACE_DETAIL_DATA_W), wintypes.DWORD,
    ctypes.POINTER(wintypes.DWORD), ctypes.POINTER(SP_DEVINFO_DATA)]
setupapi.SetupDiDestroyDeviceInfoList.restype = wintypes.BOOL
setupapi.SetupDiDestroyDeviceInfoList.argtypes = [wintypes.HANDLE]

hid.HidD_GetHidGuid.restype = None
hid.HidD_GetHidGuid.argtypes = [ctypes.POINTER(GUID)]
hid.HidD_GetAttributes.restype = wintypes.BOOLEAN
hid.HidD_GetAttributes.argtypes = [wintypes.HANDLE, ctypes.POINTER(HIDD_ATTRIBUTES)]
hid.HidD_GetPreparsedData.restype = wintypes.BOOLEAN
hid.HidD_GetPreparsedData.argtypes = [wintypes.HANDLE, ctypes.POINTER(ctypes.c_void_p)]
hid.HidD_FreePreparsedData.restype = wintypes.BOOLEAN
hid.HidD_FreePreparsedData.argtypes = [ctypes.c_void_p]
hid.HidP_GetCaps.restype = ctypes.c_long
hid.HidP_GetCaps.argtypes = [ctypes.c_void_p, ctypes.POINTER(HIDP_CAPS)]
kernel32.DeviceIoControl.restype = wintypes.BOOL
kernel32.DeviceIoControl.argtypes = [wintypes.HANDLE, wintypes.DWORD, ctypes.c_void_p,
                                     wintypes.DWORD, ctypes.c_void_p, wintypes.DWORD,
                                     ctypes.POINTER(wintypes.DWORD), ctypes.c_void_p]
# IOCTL_HID_GET_REPORT_DESCRIPTOR = CTL_CODE(0x0B, 1, METHOD_NEITHER=3, FILE_ANY_ACCESS=0)
IOCTL_HID_GET_REPORT_DESCRIPTOR = 0x000B0007
hid.HidD_GetProductString.restype = wintypes.BOOLEAN
hid.HidD_GetProductString.argtypes = [wintypes.HANDLE, ctypes.c_void_p, wintypes.ULONG]
hid.HidD_GetManufacturerString.restype = wintypes.BOOLEAN
hid.HidD_GetManufacturerString.argtypes = [wintypes.HANDLE, ctypes.c_void_p, wintypes.ULONG]

kernel32.CreateFileW.restype = wintypes.HANDLE
kernel32.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD,
                                 ctypes.c_void_p, wintypes.DWORD, wintypes.DWORD,
                                 wintypes.HANDLE]
kernel32.CloseHandle.restype = wintypes.BOOL
kernel32.CloseHandle.argtypes = [wintypes.HANDLE]


def wide_string(fn, handle):
    buf = ctypes.create_string_buffer(1024)
    try:
        if fn(handle, buf, 1024):
            return buf.raw.decode("utf-16-le", "ignore").split("\x00")[0]
    except Exception:
        pass
    return ""


def parse_descriptor(raw: bytes):
    """极简 HID 报告描述符解析：提取 Usage Page / Usage / Report ID / 报告长度。"""
    events = []
    i = 0
    usage_page = 0
    report_id = 0
    report_count = 0
    report_size = 0
    while i < len(raw):
        prefix = raw[i]
        if prefix == 0xFE:  # long item
            size = raw[i + 1] if i + 1 < len(raw) else 0
            i += 3 + size
            continue
        size = {0: 0, 1: 1, 2: 2, 3: 4}[prefix & 0x03]
        typ = (prefix >> 2) & 0x03
        tag = (prefix >> 4) & 0x0F
        data = raw[i + 1:i + 1 + size]
        val = int.from_bytes(data, "little") if data else 0
        if typ == 1:  # Global
            if tag == 0x0:
                usage_page = val
            elif tag == 0x7:
                report_size = val
            elif tag == 0x8:
                report_id = val
            elif tag == 0x9:
                report_count = val
            elif tag == 0xA:
                events.append(("collection", f"type={val}"))
            elif tag == 0xB:
                events.append(("end_collection", ""))
        elif typ == 2:  # Local
            if tag == 0x0:
                events.append(("usage", f"page=0x{usage_page:04X} usage=0x{val:04X}"))
        elif typ == 0:  # Main
            kind = {0x8: "INPUT", 0x9: "OUTPUT", 0xB: "FEATURE", 0xA: "COLLECTION",
                    0xC: "END_COLLECTION"}.get(tag, f"main{tag}")
            if kind in ("INPUT", "OUTPUT", "FEATURE"):
                bits = report_count * report_size
                events.append((kind, f"id={report_id} bits={bits} bytes={(bits + 7)//8}"))
        i += 1 + size
    return events


def main():
    guid = GUID()
    hid.HidD_GetHidGuid(ctypes.byref(guid))
    hdev = setupapi.SetupDiGetClassDevsW(ctypes.byref(guid), None, None,
                                         DIGCF_PRESENT | DIGCF_DEVICEINTERFACE)
    if hdev == INVALID_HANDLE_VALUE:
        print("SetupDiGetClassDevs failed")
        return

    idx = 0
    while True:
        iface = SP_DEVICE_INTERFACE_DATA()
        iface.cbSize = ctypes.sizeof(SP_DEVICE_INTERFACE_DATA)
        if not setupapi.SetupDiEnumDeviceInterfaces(hdev, None, ctypes.byref(guid), idx,
                                                    ctypes.byref(iface)):
            break
        idx += 1
        raw_detail = ctypes.create_string_buffer(2048)
        detail = ctypes.cast(raw_detail, ctypes.POINTER(SP_DEVICE_INTERFACE_DETAIL_DATA_W))
        detail.contents.cbSize = 8 if ctypes.sizeof(ctypes.c_void_p) == 8 else 6
        need = wintypes.DWORD()
        devinfo = SP_DEVINFO_DATA()
        devinfo.cbSize = ctypes.sizeof(SP_DEVINFO_DATA)
        ok = setupapi.SetupDiGetDeviceInterfaceDetailW(
            hdev, ctypes.byref(iface), detail, 2048,
            ctypes.byref(need), ctypes.byref(devinfo))
        if not ok:
            continue
        path = ctypes.wstring_at(ctypes.addressof(raw_detail) + ctypes.sizeof(wintypes.DWORD))

        h = kernel32.CreateFileW(path, GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE,
                                 None, OPEN_EXISTING, 0, None)
        if h == INVALID_HANDLE_VALUE or h == -1:
            continue
        try:
            attr = HIDD_ATTRIBUTES()
            attr.Size = ctypes.sizeof(HIDD_ATTRIBUTES)
            if not hid.HidD_GetAttributes(h, ctypes.byref(attr)):
                continue
            if attr.VendorID != 0x2D99:
                continue  # 只关心漫步者
            product = wide_string(hid.HidD_GetProductString, h)
            mfr = wide_string(hid.HidD_GetManufacturerString, h)

            pp = ctypes.c_void_p()
            caps = HIDP_CAPS()
            has_caps = False
            if hid.HidD_GetPreparsedData(h, ctypes.byref(pp)):
                if hid.HidP_GetCaps(pp, ctypes.byref(caps)) == 0x00110000:
                    has_caps = True
                hid.HidD_FreePreparsedData(pp)

            print("=" * 78)
            print(f"VID=0x{attr.VendorID:04X} PID=0x{attr.ProductID:04X} "
                  f"VER=0x{attr.VersionNumber:04X}")
            print(f"Product: {product}")
            print(f"Manufacturer: {mfr}")
            print(f"Path: {path}")
            if has_caps:
                print(f"UsagePage=0x{caps.UsagePage:04X} Usage=0x{caps.Usage:04X} "
                      f"In={caps.InputReportByteLength} Out={caps.OutputReportByteLength} "
                      f"Feat={caps.FeatureReportByteLength}")
            rdbuf = ctypes.create_string_buffer(4096)
            returned = wintypes.DWORD(0)
            got = False
            for code in (0x000B0007, 0x000B01F5, 0x000B0003):
                for mode in ("both", "in"):
                    ctypes.memset(rdbuf, 0, 4096)
                    returned.value = 0
                    if mode == "both":
                        ok = kernel32.DeviceIoControl(h, code, rdbuf, 4096, rdbuf, 4096,
                                                      ctypes.byref(returned), None)
                    else:
                        ok = kernel32.DeviceIoControl(h, code, rdbuf, 4096, None, 0,
                                                      ctypes.byref(returned), None)
                    if ok and returned.value:
                        raw = rdbuf.raw[:returned.value]
                        print(f"ReportDescriptor via 0x{code:08X}/{mode} ({len(raw)} bytes):")
                        print("  " + raw.hex(' '))
                        for kind, info in parse_descriptor(raw):
                            print(f"   {kind:14s} {info}")
                        got = True
                        break
                if got:
                    break
            if not got:
                print(f"ReportDescriptor unavailable (err={ctypes.get_last_error()})")
        finally:
            kernel32.CloseHandle(h)
    setupapi.SetupDiDestroyDeviceInfoList(hdev)


if __name__ == "__main__":
    main()
