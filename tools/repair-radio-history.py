"""Supplement device radio GPS from a previously saved measured-track export.

All device mutations are in tools/device/repair-radio-history.sh. Source exports,
backups and raw results stay private; only counts are printed.
"""
import argparse
import hashlib
import json
import pathlib
import re
import subprocess
import datetime


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serial',required=True)
    parser.add_argument('--measured-track',required=True,type=pathlib.Path)
    args=parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9._:-]+',args.serial):
        parser.error('Invalid serial')
    raw=args.measured_track.read_bytes()
    value=json.loads(raw)
    if len(raw)>32*1024*1024 or value.get('status')!='available' or value.get('data',{}).get('source')!='stock-recording-nmea':
        parser.error('A measured recording-track export is required')
    root=pathlib.Path(__file__).resolve().parents[1]
    directory=root/'tmp'/('journey-import-'+datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ'))
    directory.mkdir(exist_ok=False)
    (directory/'measured-track.private.json').write_bytes(raw)
    adb=['adb','-s',args.serial]
    result=subprocess.run(adb+['shell','id -u'],capture_output=True,timeout=15,check=True)
    if result.stdout.strip()!=b'0':
        raise RuntimeError('Root device transport required')
    target='/tmp/ncc-radio-measured-track.json'
    result=subprocess.run(adb+['push',str(directory/'measured-track.private.json'),target],capture_output=True,timeout=60,check=True)
    (directory/'transfer.private.txt').write_bytes(result.stdout+result.stderr)
    readback=subprocess.check_output(adb+['shell','sha256sum',target],timeout=15)
    if readback.split()[0].decode()!=hashlib.sha256(raw).hexdigest():
        raise RuntimeError('Measured track transfer read-back mismatch')
    # /tmp is cleared at reboot. Stage the current, reproducible repair script
    # independently of the last software deployment's temporary bundle.
    script=root/'tools/device/repair-radio-history.sh'
    script_raw=script.read_bytes().replace(b'\r\n',b'\n')
    staged=directory/'repair-radio-history.sh'
    staged.write_bytes(script_raw)
    remote='/tmp/ncc-radio-repair.sh'
    subprocess.run(adb+['push',str(staged),remote],capture_output=True,timeout=30,check=True)
    readback=subprocess.check_output(adb+['shell','sha256sum',remote],timeout=15)
    if readback.split()[0].decode()!=hashlib.sha256(script_raw).hexdigest():
        raise RuntimeError('Repair script transfer read-back mismatch')
    command='/bin/sh '+remote+' apply /tmp/ncc-radio-measured-track.json; result=$?; printf "\\nNCC_RADIO_REPAIR_EXIT=%s\\n" "$result"; exit "$result"'
    result=subprocess.run(adb+['shell',command],capture_output=True,timeout=180)
    (directory/'result.private.txt').write_bytes(result.stdout+result.stderr)
    output=result.stdout.decode('utf8',errors='replace').replace('\r','')
    if result.returncode or not re.search(r'NCC_RADIO_REPAIR_EXIT=0\s*$',output):
        raise RuntimeError('Measured radio repair failed; inspect '+str(directory))
    reports=[json.loads(line) for line in output.splitlines() if line.startswith('{')]
    if len(reports)!=2 or reports[-1]['matched']!=0:
        raise RuntimeError('Repair read-back report missing')
    print(json.dumps({'applied':reports[0],'readback':reports[-1]}))
    print('Private evidence:',directory)


if __name__=='__main__':
    main()
