#!/bin/sh
# Bench only. Use the stock BT manager in its idle state, perform a short test,
# and read back both Bluetooth state and the original service invocations.
# Optional --unblock-bt temporarily opens the unique bt_power rfkill switch.
set -eu
exec python3 - "$@" <<'PY'
import hashlib, os, pathlib, re, stat, struct, subprocess, sys, time, signal

BASELINE_SHA='3a57830dbc5e3ad26355bb14ca28141f8e82eba94b10b8b619dabea60901ec06'
owner='/usr/local/bin/NEventWatcherCom'
if hashlib.sha256(open(owner,'rb').read()).hexdigest()!=BASELINE_SHA:
    raise SystemExit('stock service fingerprint mismatch')
pid=int(subprocess.check_output(['pidof','NEventWatcherCom']).strip())
exe=os.readlink('/proc/{}/exe'.format(pid))
if exe!=owner:raise SystemExit('stock service executable mismatch')
base = None
for row in open('/proc/{}/maps'.format(pid)):
    a=row.split()
    if len(a)>5 and a[2]=='00000000' and a[5]==owner:
        base=int(a[0].split('-')[0],16)
        break
if base is None:raise SystemExit('stock mapping unavailable')
bus_path='/tmp/dbus_session_bus_address'
if not stat.S_ISREG(os.lstat(bus_path).st_mode) or os.stat(bus_path).st_uid!=0:
    raise SystemExit('invalid stock bus address source')
address=re.search(r"^DBUS_SESSION_BUS_ADDRESS='(unix:[^'\n;]+)'",open(bus_path).read(),re.MULTILINE).group(1)
bus=['busctl','--address='+address,'--timeout=3s']
iface='SPF.EventBase.v1_0'
units=['NEventWatcherCom.service','app-@Communication.service','app-@DriveRec.service','app-@ImageRecog.service','app-@Voice.service','qmmf-server.service']
def lifecycle():
    return subprocess.check_output(['systemctl','show','-p','Id,MainPID,InvocationID,ActiveState']+units,timeout=5)

def read_states():
    if int(subprocess.check_output(['pidof','NEventWatcherCom']).strip())!=pid:
        raise RuntimeError('stock service restarted')
    with open('/proc/{}/mem'.format(pid),'rb',0) as f:
        def read(addr,fmt):
            f.seek(addr)
            return struct.unpack(fmt,f.read(struct.calcsize(fmt)))[0]
        mgr=read(base+0x1e62d0,'<Q')
        ble=read(base+0x1e6290,'<Q')
        if not mgr or not ble: raise RuntimeError('stock Bluetooth owner unavailable')
        return read(mgr+0x84,'<I'),read(ble+0x48,'<I')

def wait_until(check,seconds,description):
    end=time.monotonic()+seconds
    while time.monotonic()<end:
        if check(read_states()): return
        time.sleep(.2)
    raise RuntimeError(description)

def call(handler,function,data):
    service='SPF.EventBase.{}.Stub'.format(handler)
    path='/SPF/EventBase/{}'.format(handler)
    command=bus+['call',service,path,iface,'CallFunction','uuuauayu','1',str(os.getpid()),str(function),'0',str(len(data))]+[str(n) for n in data]+['1']
    out=subprocess.check_output(command,stderr=subprocess.DEVNULL,timeout=5).split()
    if len(out)!=4 or out[:2]!=[b'ayau',b'1'] or out[2] not in (b'0',b'1') or out[3]!=b'0':
        raise RuntimeError('unexpected stock Bluetooth acknowledgement')
    return out[2]==b'1'

def set_bt(on):
    # Stock Communication uses status 0/1, the default identity and reset type 0.
    data=struct.pack('<IIQQIB6sxI',1,0,0,0,int(on),1,b'\0'*6,0)
    return call('NPBTManagerHandler',0,data)
def set_ble(on):
    f=0 if on else 1
    return call('NPBTLEHandler',f,struct.pack('<III',0,f,f))

args=sys.argv[1:]
rfkill_state=None
rfkill_changed=False
if args[:1]==['--unblock-bt']:
    args=args[1:]
    devices=[p for p in pathlib.Path('/sys/class/rfkill').glob('rfkill*')
             if (p/'name').read_text().strip()=='bt_power' and (p/'type').read_text().strip()=='bluetooth']
    if len(devices)!=1:
        raise SystemExit('unique stock Bluetooth rfkill switch unavailable')
    rfkill=devices[0]
    rfkill_state=rfkill/'state'
    original=rfkill_state.read_text().strip()
    if original not in ('0','1') or (rfkill/'hard').read_text().strip()!='0':
        raise SystemExit('Bluetooth rfkill switch is not writable without a hardware change')

before=lifecycle()
start=read_states()
if start!=(0,0):
    raise SystemExit('trial expects an idle stock Bluetooth stack')
bt_requested=False
ble_requested=False
def cancel(*args):
    raise KeyboardInterrupt()
signal.signal(signal.SIGTERM,cancel)
signal.signal(signal.SIGINT,cancel)
errors=[]
try:
    if rfkill_state and original=='0':
        rfkill_changed=True
        rfkill_state.write_text('1\n')
        if rfkill_state.read_text().strip()!='1':
            raise RuntimeError('Bluetooth rfkill unblock did not take effect')
        print('STOCK_BT_RFKILL_UNBLOCKED',flush=True)
    bt_requested=True
    if not set_bt(True):
        bt_requested=False
        raise RuntimeError('stock BT start rejected')
    wait_until(lambda s:s[0]!=0,10,'stock BT startup timed out')
    print('STOCK_BT_STARTED',flush=True)
    ble_requested=True
    if not set_ble(True):
        ble_requested=False
        raise RuntimeError('stock BLE initialization rejected')
    wait_until(lambda s:s[1]==1,6,'stock BLE initialization timed out')
    print('STOCK_BLE_READY',flush=True)
    if args:
        child=subprocess.Popen(args)
        try:
            result=child.wait(timeout=45)
            if result:
                errors.append('receiver trial exited {}'.format(result))
        finally:
            if child.poll() is None:
                child.terminate()
                child.wait(timeout=4)
except (KeyboardInterrupt, OSError, RuntimeError, subprocess.SubprocessError) as error:
    errors.append(str(error) or 'cancelled')
finally:
    signal.signal(signal.SIGTERM,signal.SIG_IGN)
    signal.signal(signal.SIGINT,signal.SIG_IGN)
    try:
        if ble_requested and read_states()[1]==1:
            if not set_ble(False):
                raise RuntimeError('stock BLE release rejected')
            wait_until(lambda s:s[1]==0,8,'stock BLE release timed out')
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        errors.append(str(error))
    try:
        if bt_requested:
            if not set_bt(False):
                if read_states()[0]!=0:
                    raise RuntimeError('stock BT release rejected')
            wait_until(lambda s:s[0]==0,10,'stock BT release timed out')
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        errors.append(str(error))
    try:
        if rfkill_state:
            if rfkill_changed:
                rfkill_state.write_text(original+'\n')
            if rfkill_state.read_text().strip()!=original:
                raise RuntimeError('Bluetooth rfkill state differs from baseline')
            print('STOCK_BT_RFKILL_RESTORED',flush=True)
        if read_states()!=start:
            raise RuntimeError('stock Bluetooth state differs from baseline')
        if lifecycle()!=before:
            raise RuntimeError('stock process state differs from baseline')
        print('STOCK_BT_RESTORED',flush=True)
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        errors.append(str(error))
for error in errors:
    print('TRIAL_ERROR '+error,flush=True)
print('STOCK_TRIAL_COMPLETE',flush=True)
sys.exit(1 if errors else 0)
PY
