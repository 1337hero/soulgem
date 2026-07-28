"""Build a TTS voice-reference wav from a Skyrim voice type's dialogue.

The clone reference wants ~60s of clean 24 kHz mono speech from one speaker.
Skyrim ships every line as .fuz (FUZE header + lip data + xWMA) inside the
voice BSAs, so: pick the mid-length lines of one voice type, decode, concat.

    python3 voice_ref.py dlc1seranavoice souls/serana/voice_ref.wav
"""
import argparse
import os
import struct
import subprocess
import tempfile

from bsa import BSA

DATA = '/home/mikekey/.local/share/Steam/steamapps/common/Skyrim Special Edition/Data'
# UHDAP re-encodes vanilla voices at higher bitrate; fall back to the base BSA.
BSAS = ['UHDAP - en0.bsa', 'UHDAP - en1.bsa', 'UHDAP - en2.bsa', 'UHDAP - en3.bsa',
        'UHDAP - en4.bsa', 'Skyrim - Voices_en0.bsa']


def decode(fuz, out):
    lip = struct.unpack('<I', fuz[8:12])[0]  # FUZE, u32 version, u32 lipsize
    xwm = out + '.xwm'
    open(xwm, 'wb').write(fuz[12 + lip:])
    subprocess.run(['ffmpeg', '-v', 'error', '-y', '-i', xwm,
                    '-ar', '24000', '-ac', '1', '-c:a', 'pcm_s16le', out], check=True)
    os.remove(xwm)
    return float(subprocess.run(
        ['ffprobe', '-v', 'error', '-show_entries', 'format=duration',
         '-of', 'csv=p=0', out], capture_output=True, text=True, check=True).stdout)


def main(voice, out, seconds, lo, hi):
    lines = []
    for name in BSAS:
        path = os.path.join(DATA, name)
        if not os.path.exists(path):
            continue
        bsa = BSA(path)
        hits = [f for f in bsa.files if voice.lower() in f.lower()]
        if hits:
            print(f'{name}: {len(hits)} lines')
            lines = [(bsa, f) for f in hits]
            break
    if not lines:
        raise SystemExit(f'no lines for voice type {voice!r} in {DATA}')

    # longest-first: long lines are quest dialogue, short ones are grunts/greetings
    lines.sort(key=lambda bf: -len(bf[0].read(bf[1])))
    with tempfile.TemporaryDirectory() as tmp:
        picked, total = [], 0.0
        for i, (bsa, f) in enumerate(lines):
            if total >= seconds:
                break
            wav = os.path.join(tmp, f'{i:04d}.wav')
            dur = decode(bsa.read(f), wav)
            if not lo <= dur <= hi:
                continue
            picked.append(wav)
            total += dur
            print(f'  + {dur:4.1f}s  {os.path.basename(f)}')
        listing = os.path.join(tmp, 'list.txt')
        open(listing, 'w').write(''.join(f"file '{w}'\n" for w in picked))
        subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'concat', '-safe', '0',
                        '-i', listing, '-t', str(seconds), '-ar', '24000', '-ac', '1',
                        '-c:a', 'pcm_s16le', out], check=True)
    print(f'\nwrote {out}: {seconds}s from {len(picked)} lines')


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('voice', help='voice type dir, e.g. dlc1seranavoice')
    p.add_argument('out')
    p.add_argument('--seconds', type=float, default=60.0)
    p.add_argument('--min', dest='lo', type=float, default=3.0, help='shortest line to keep')
    p.add_argument('--max', dest='hi', type=float, default=9.0, help='longest line to keep')
    main(**vars(p.parse_args()))
