"""Minimal SSE BSA (v105) reader — extract files by path. lz4 via /usr/bin/lz4."""
import os
import struct
import subprocess


class BSA:
    def __init__(self, path):
        self.f = open(path, 'rb')
        magic, version, offset, flags, folder_count, file_count, \
            folder_name_len, file_name_len, file_flags = struct.unpack('<4sIIIIIIII', self.f.read(36))
        assert magic == b'BSA\x00' and version == 105, (magic, version)
        self.compressed_default = bool(flags & 0x4)
        self.embed_names = bool(flags & 0x100)
        folders = []
        for _ in range(folder_count):
            name_hash, count, _pad, off = struct.unpack('<QIIQ', self.f.read(24))
            folders.append((count, off))
        self.files = {}  # 'folder/file' -> (offset, size, compressed)
        entries = []
        for count, _off in folders:
            nlen = self.f.read(1)[0]
            fname = self.f.read(nlen)[:-1].decode('latin-1').lower().replace('\\', '/')
            for _ in range(count):
                _h, size, off = struct.unpack('<QII', self.f.read(16))
                compressed = self.compressed_default != bool(size & 0x40000000)
                entries.append((fname, off, size & 0x3FFFFFFF, compressed))
        name_block = self.f.read(file_name_len)
        names = name_block.split(b'\x00')[:file_count]
        for (folder, off, size, comp), fn in zip(entries, names):
            self.files[folder + '/' + fn.decode('latin-1').lower()] = (off, size, comp)

    def read(self, path):
        off, size, comp = self.files[path.lower().replace('\\', '/')]
        self.f.seek(off)
        data = self.f.read(size)
        if self.embed_names:
            nlen = data[0]
            data = data[1 + nlen:]
        if comp:
            data = data[4:]  # u32 uncompressed size
            p = subprocess.run(['lz4', '-d', '-c'], input=data, capture_output=True)
            data = p.stdout
        return data


if __name__ == '__main__':
    import sys
    bsa = BSA(sys.argv[1])
    if len(sys.argv) == 2:
        for k in bsa.files:
            print(k)
    else:
        pattern = sys.argv[2].lower()
        for k in bsa.files:
            if pattern in k:
                print(k)
