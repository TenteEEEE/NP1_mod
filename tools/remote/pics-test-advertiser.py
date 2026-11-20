"""Broadcast a PICS-shaped fixture from a free BlueZ adapter.

The reserved company ID and eight-byte nonce identify the test frames.
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
    def __init__(self, bus, timeout, stable_address):
        super().__init__(bus, PATH)
        self.payload = b""
        self.timeout = timeout
        self.stable_address = stable_address

    def manufacturer_data(self):
        value = dbus.ByteArray(self.payload, variant_level=1)
        return dbus.Dictionary({dbus.UInt16(MANUFACTURER): value}, signature="qv")

    @dbus.service.method(PROPS, in_signature="s", out_signature="a{sv}")
    def GetAll(self, interface):
        if interface != ADV:
            raise dbus.exceptions.DBusException("unexpected interface")
        properties = {
            "Type": dbus.String("peripheral" if self.stable_address else "broadcast"),
            "Timeout": dbus.UInt16(self.timeout),
            "MinInterval": dbus.UInt32(100),
            "MaxInterval": dbus.UInt32(100),
            "ManufacturerData": self.manufacturer_data(),
        }
        if self.stable_address:
            properties["Discoverable"] = dbus.Boolean(False)
        return properties

    @dbus.service.signal(PROPS, signature="sa{sv}as")
    def PropertiesChanged(self, interface, changed, invalidated):
        pass

    @dbus.service.method(ADV, in_signature="", out_signature="")
    def Release(self):
        pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag", type=bytes.fromhex)
    parser.add_argument("--seconds", type=int, default=12, choices=range(4, 601), metavar="4..600")
    parser.add_argument("--cycle-seconds", type=int, choices=range(4, 61), metavar="4..60")
    parser.add_argument("--stable-address", action="store_true", help="use a temporary, non-discoverable connectable advertisement")
    opts = parser.parse_args()
    if len(opts.tag) != 8:
        parser.error("An 8-byte test tag is required")

    DBusGMainLoop(set_as_default=True)
    bus = dbus.SystemBus()
    adapter = bus.get_object("org.bluez", "/org/bluez/hci0")
    manager = dbus.Interface(adapter, "org.bluez.LEAdvertisingManager1")
    props = dbus.Interface(adapter, PROPS)
    if int(props.Get(manager.dbus_interface, "ActiveInstances")):
        raise SystemExit("Bluetooth advertising is occupied")

    loop = GLib.MainLoop()
    beacon = Beacon(bus, opts.seconds + 2, opts.stable_address)
    started = time.monotonic()
    cycle = opts.cycle_seconds or opts.seconds
    counter = 0
    last_state = None
    active = False
    failed = False

    def end():
        nonlocal active
        if active:
            try:
                manager.UnregisterAdvertisement(PATH)
            except dbus.DBusException:
                pass
            active = False
        loop.quit()
        return False

    def fail(error):
        nonlocal failed
        failed = True
        print("ADVERTISEMENT_ERROR " + error.get_dbus_name(), flush=True)
        end()

    def update_payload(elapsed):
        nonlocal counter, last_state
        state = 0x81 if elapsed % cycle < cycle * 0.60 else 0x83
        if state != last_state:
            print("TEST_GREEN_PHASE" if state == 0x83 else "TEST_RED_PHASE", counter, flush=True)
            last_state = state
        beacon.payload = opts.tag + bytes([0, 0, 2, counter % 256, 0, 0, 0, 0, 0, 1, state])
        counter += 1

    def advance():
        elapsed = time.monotonic() - started
        if elapsed >= opts.seconds:
            return end()
        update_payload(elapsed)
        beacon.PropertiesChanged(ADV, {"ManufacturerData": beacon.manufacturer_data()}, [])
        return True

    def registered():
        nonlocal active
        active = True
        print("ADVERTISEMENT_ACTIVE", flush=True)
        GLib.timeout_add(450, advance)

    signal.signal(signal.SIGTERM, lambda *_: GLib.idle_add(end))
    signal.signal(signal.SIGINT, lambda *_: GLib.idle_add(end))
    update_payload(0)
    manager.RegisterAdvertisement(PATH, dbus.Dictionary({}, signature="sv"), reply_handler=registered, error_handler=fail)
    loop.run()
    print(f"ADVERTISEMENT_STOPPED count={counter}", flush=True)
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(main())
