#!/usr/bin/env python3
"""Writes a Windows resource object (.syso) with the app icon, a version block and a manifest.
The Go linker picks it up automatically for GOOS=windows builds, so the exe shows the Sharkoxy icon
and publisher details in Explorer and the Properties dialog, and runs without a UAC prompt.
usage: mksyso.py out.syso app.ico "File description" VERSION [--setup]"""
import struct, sys

def pad4(b): return b + b"\0" * (-len(b) % 4)
def u16z(s): return s.encode("utf-16-le") + b"\0\0"

def vs_node(key, value=b"", text=False, children=b"", wvl=None):
    k = u16z(key)
    head = struct.pack("<HHH", 0, len(value) // 2 if text and wvl is None else (wvl if wvl is not None else len(value)), 1 if text else 0)
    body = pad4(head + k) + pad4(value) if value else pad4(head + k)
    body += children
    return struct.pack("<H", len(body)) + body[2:]

def version_info(desc, ver, company, product, copyright_, orig):
    a, b, c = (list(map(int, ver.split("."))) + [0, 0, 0])[:3]
    fixed = struct.pack("<13I", 0xFEEF04BD, 0x10000, (a << 16) | b, (c << 16), (a << 16) | b, (c << 16), 0x3F, 0, 0x40004, 1, 0, 0, 0)
    strings = [("CompanyName", company), ("FileDescription", desc), ("FileVersion", ver + ".0"), ("InternalName", product),
               ("LegalCopyright", copyright_), ("OriginalFilename", orig), ("ProductName", product), ("ProductVersion", ver + ".0")]
    st = b"".join(pad4(vs_node(k, u16z(v), text=True)) for k, v in strings)
    table = vs_node("040904B0", children=st, text=True)
    sfi = vs_node("StringFileInfo", children=pad4(table), text=True)
    var = vs_node("Translation", struct.pack("<HH", 0x409, 0x4B0), text=False, wvl=4)
    vfi = vs_node("VarFileInfo", children=pad4(var), text=True)
    return vs_node("VS_VERSION_INFO", fixed, text=False, wvl=52, children=pad4(sfi) + pad4(vfi))

MANIFEST = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="Sharkoxy.%s" version="%s.0" processorArchitecture="amd64"/>
  <description>%s</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3"><security><requestedPrivileges>
    <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
  </requestedPrivileges></security></trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1"><application>
    <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
    <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
    <supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
  </application></compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings>
    <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
    <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2,PerMonitor</dpiAwareness>
    <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
  </windowsSettings></application>
</assembly>
"""

def icons(path):
    d = open(path, "rb").read()
    _, _, n = struct.unpack_from("<HHH", d, 0)
    out = []
    for i in range(n):
        w, h, cc, _, pl, bc, size, off = struct.unpack_from("<BBBBHHII", d, 6 + 16 * i)
        out.append((w, h, cc, pl, bc, d[off:off + size]))
    return out

def build(res):
    """res: list of (type, name, bytes). Returns (section bytes, relocation offsets)."""
    types = {}
    for t, n, data in res: types.setdefault(t, {})[n] = data
    tids = sorted(types)
    # layout sizes
    root = 16 + 8 * len(tids)
    sizes_t = {t: 16 + 8 * len(types[t]) for t in tids}
    sizes_n = 16 + 8  # one language per name
    off = root; t_off = {}
    for t in tids: t_off[t] = off; off += sizes_t[t]
    n_off = {}
    for t in tids:
        for n in sorted(types[t]): n_off[(t, n)] = off; off += sizes_n
    d_off = {}
    for t in tids:
        for n in sorted(types[t]): d_off[(t, n)] = off; off += 16
    blob = bytearray(off); relocs = []
    def dirhdr(pos, nid): struct.pack_into("<IIHHHH", blob, pos, 0, 0, 0, 0, 0, nid)
    dirhdr(0, len(tids))
    for i, t in enumerate(tids): struct.pack_into("<II", blob, 16 + 8 * i, t, 0x80000000 | t_off[t])
    for t in tids:
        names = sorted(types[t]); dirhdr(t_off[t], len(names))
        for i, n in enumerate(names): struct.pack_into("<II", blob, t_off[t] + 16 + 8 * i, n, 0x80000000 | n_off[(t, n)])
        for n in names:
            dirhdr(n_off[(t, n)], 1); struct.pack_into("<II", blob, n_off[(t, n)] + 16, 0x409, d_off[(t, n)])
    for t in tids:
        for n in sorted(types[t]):
            data = types[t][n]; pos = d_off[(t, n)]
            while len(blob) % 4: blob.append(0)
            start = len(blob); blob += data
            struct.pack_into("<IIII", blob, pos, start, len(data), 0, 0); relocs.append(pos)
    while len(blob) % 4: blob.append(0)
    return bytes(blob), relocs

def main():
    out, ico, desc, ver = sys.argv[1:5]
    setup = "--setup" in sys.argv
    res = []; grp = struct.pack("<HHH", 0, 1, 0); entries = b""; ims = icons(ico)
    for i, (w, h, cc, pl, bc, data) in enumerate(ims, 1):
        res.append((3, i, data)); entries += struct.pack("<BBBBHHIH", w, h, cc, 0, pl, bc, len(data), i)
    res.append((14, 1, struct.pack("<HHH", 0, 1, len(ims)) + entries))
    res.append((16, 1, version_info(desc, ver, "Shahriar Rahman", "Sharkoxy", "Copyright (C) Shahriar Rahman. GPL-3.0",
                                    "Sharkoxy-Setup.exe" if setup else "Sharkoxy.exe")))
    res.append((24, 1, (MANIFEST % ("Setup" if setup else "App", ver, desc)).encode()))
    sec, relocs = build(res)
    raw = 20 + 40
    rel = b"".join(struct.pack("<IIH", r, 0, 3) for r in relocs)
    f = struct.pack("<HHIIIHH", 0x8664, 1, 0, raw + len(sec) + len(rel), 1, 0, 0)
    s = struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(sec), raw, raw + len(sec), 0, len(relocs), 0, 0x40000040)
    sym = struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)
    open(out, "wb").write(f + s + sec + rel + sym + struct.pack("<I", 4))
main()
