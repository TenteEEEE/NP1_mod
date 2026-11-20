"""Broadcast a short bench PICS-shaped fixture from a free BlueZ adapter.

The reserved company ID and an explicit eight-byte nonce keep these frames
out of production PICS receivers. Run on the test transmitter, not the NP1.
"""

import argparse
import signal
import time
import dbus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib

ADV = "org.bluez.LEAdvertisement1"
PROPS = "org.freedesktop.DBus.Properties"
PATH = "/np1_private_test_pics"
MANUFACTURER = 0xffff
class Beacon(dbus.service.Object):
    def __init__(self, bus):
        super().__init__(bus, PATH)
        self.payload = b""
    @dbus.service.method(PROPS, in_signature="s", out_signature="a{sv}")
    def GetAll(self, interface):
        if interface != ADV:
            raise dbus.exceptions.DBusException("unexpected interface")
        return {
            "Type": dbus.String("broadcast"),
            "Timeout": dbus.UInt16(2),
            "ManufacturerData": dbus.Dictionary({dbus.UInt16(MANUFACTURER): dbus.ByteArray(self.payload, variant_level=1)}, signature="qv"),
        }
    @dbus.service.method(ADV, in_signature="", out_signature="")
    def Release(self):
        pass

args = argparse.ArgumentParser()
args.add_argument("tag")
args.add_argument("--seconds", type=int, default=12, choices=range(4, 21))
opts = args.parse_args()
tag = bytes.fromhex(opts.tag)
if len(tag)!=8:
    raise SystemExit("An 8-byte test tag is required")
DBusGMainLoop(set_as_default=True)
bus=dbus.SystemBus()
adapter=dbus.Interface(bus.get_object("org.bluez", "/org/bluez/hci0"),"org.bluez.LEAdvertisingManager1")
dbusprops=dbus.Interface(bus.get_object("org.bluez", "/org/bluez/hci0"),PROPS)
if int(dbusprops.Get("org.bluez.LEAdvertisingManager1","ActiveInstances")):
    raise SystemExit("Bluetooth advertising is occupied")
beacon=Beacon(bus)
loop=GLib.MainLoop()
counter=0
active=False
registered_at=time.monotonic()
deadline=registered_at+opts.seconds
last_seen=False
failed=False
def end():
    global active
    if active:
        try:
            adapter.UnregisterAdvertisement(PATH)
        except dbus.DBusException:
            pass
        active = False
    loop.quit()
    return False
def fail(error):
    global failed
    failed = True
    print("ADVERTISEMENT_ERROR "+error.get_dbus_name(), flush=True)
    end()
def advancing():
    if time.monotonic()>=deadline:
        return end()
    adapter.UnregisterAdvertisement(PATH, reply_handler=unregistered, error_handler=fail)
    return False
def unregistered():
    global active
    active=False
    register()
def registered():
    global active
    active=True
    if counter == 1:
        print("ADVERTISEMENT_ACTIVE", flush=True)
    if time.monotonic()>=deadline:
        end()
    else:
        GLib.timeout_add(450, advancing)
def register():
    global counter,last_seen
    state = 0x81 if time.monotonic()-registered_at < opts.seconds*0.60 else 0x83
    if state==0x83 and not last_seen:
        print("TEST_GREEN_PHASE",flush=True)
        last_seen=True
    beacon.payload=tag+bytes([0,0,2,counter%256,0,0,0,0,0,1,state])
    counter+=1
    adapter.RegisterAdvertisement(PATH, dbus.Dictionary({}, signature="sv"),reply_handler=registered,error_handler=fail)
signal.signal(signal.SIGTERM, lambda *_: GLib.idle_add(end))
signal.signal(signal.SIGINT, lambda *_: GLib.idle_add(end))
register()
loop.run()
print("ADVERTISEMENT_STOPPED count={}".format(counter),flush=True)
raise SystemExit(1 if failed else 0)
