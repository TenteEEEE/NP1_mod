import hashlib
import importlib.util
import json
import os
import pathlib
import re
import signal
import stat
import subprocess
import sys
import tempfile
import time


module = importlib.util.spec_from_file_location('usb_probe', pathlib.Path(__file__).with_name('usb-wifi-ap-probe.py'))
probe = importlib.util.module_from_spec(module)
module.loader.exec_module(probe)
require = probe.require


def snapshot_changes(before, current, protected):
    def comparable(key, value):
        if key in ('routes', 'routes6'):
            return [route for route in value if not (
                route.get('protocol') == 'ra' and route.get('dev') in protected
                and re.fullmatch(r'wlo\d+', route.get('dev', '')))]
        return value

    return [key for key in before if comparable(key, before[key]) != comparable(key, current[key])]


class UplinkFixture(probe.RemoteProbe):
    def __init__(self, payload):
        super().__init__(payload)
        self.children = []
        self.rules = []
        self.veth = False
        self.network = '10.247.63.0/24'
        self.gateway = '10.247.63.1'
        self.host_side = 'ncct' + self.ns[-8:]
        self.ns_side = 'nccp' + self.ns[-8:]
        self.internet_blocked = False

    def child(self, name, args):
        with (self.runtime / (name + '.log')).open('ab') as log:
            child = subprocess.Popen(args, stdout=log, stderr=log,
                preexec_fn=self.enter_child, pass_fds=(self.testns,))
        self.children.append(child)
        return child

    def add_rule(self, table, chain, args):
        self.command(['iptables', '-w', '2', '-t', table, '-I', chain, '1'] + args)
        self.rules.append((table, chain, args))

    def start(self):
        have_spare = self.payload.get('allowSpareUSBWiFiChanges') is True
        require(have_spare, 'USB_WIFI_MUTATION_NOT_AUTHORIZED')
        ssid = self.payload['ssid'].encode('ascii')
        secret = self.payload['password'].encode('ascii')
        require(re.fullmatch(rb'ncc-field-[a-f0-9]{8}', ssid) and re.fullmatch(rb'[a-f0-9]{20}', secret), 'INVALID_TEST_IDENTITY')
        self.stage = 'preflight'
        lockfd = os.open('/run/ncc-usb-wifi-probe.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        self.lock = os.fdopen(lockfd, 'a+')
        lock = os.fstat(lockfd)
        require(lock.st_uid == 0 and stat.S_ISREG(lock.st_mode) and lock.st_nlink == 1 and lock.st_size == 0 and stat.S_IMODE(lock.st_mode) == 0o600, 'FOREIGN_LOCK_PRESERVED')
        probe.fcntl.flock(self.lock.fileno(), probe.fcntl.LOCK_EX | probe.fcntl.LOCK_NB)
        self.preflight()
        require(pathlib.Path('/proc/sys/net/ipv4/ip_forward').read_text().strip() == '1', 'HOST_FORWARDING_NOT_ALREADY_ENABLED')
        route = self.command(['ip', '-4', 'route', 'get', '1.1.1.1']).split()
        require('dev' in route and route[route.index('dev') + 1] in self.profile['protectedInterfaces'], 'HOST_UPLINK_UNEXPECTED')
        host_uplink = route[route.index('dev') + 1]
        for destination in (self.network, '198.18.63.0/30'):
            require(not self.command(['ip', '-4', 'route', 'show', 'root', destination]), 'FIXTURE_ROUTE_OCCUPIED')
        self.runtime = pathlib.Path(tempfile.mkdtemp(prefix='ncc-uplink-fixture-', dir='/run'))
        evidence = {'profile': self.profile, 'namespace': self.ns, 'hostSnapshot': self.before,
                    'originalAutoconnect': self.original_autoconnect}
        (self.runtime / 'recovery.json').write_text(json.dumps(evidence))
        self.stage = 'usb-isolation'
        self.autoconnect_changed = True
        self.command(['nmcli', 'device', 'set', self.iface, 'autoconnect', 'no'])
        self.unmanaged = True
        self.command(['nmcli', 'device', 'set', self.iface, 'managed', 'no'])
        self.command(['ip', 'link', 'set', self.iface, 'down'])
        self.identity()
        self.command(['ip', 'netns', 'add', self.ns])
        self.created = True
        self.testns = os.open('/run/netns/' + self.ns, os.O_RDONLY)
        metadata = os.fstat(self.testns)
        self.ns_identity = metadata.st_dev, metadata.st_ino
        self.command(['iw', 'phy', self.phy, 'set', 'netns', 'name', self.ns])
        self.moved = True
        self.namespace_target_owned()
        self.stage = 'isolated-NAT'
        self.command(['ip', 'link', 'add', self.host_side, 'type', 'veth', 'peer', 'name', self.ns_side, 'netns', self.ns])
        self.veth = True
        self.command(['ip', 'addr', 'add', '198.18.63.1/30', 'dev', self.host_side])
        self.command(['ip', 'link', 'set', self.host_side, 'up'])
        for command in (['ip', 'addr', 'add', '198.18.63.2/30', 'dev', self.ns_side],
                        ['ip', 'link', 'set', self.ns_side, 'up'], ['ip', 'link', 'set', 'lo', 'up'],
                        ['ip', '-4', 'route', 'add', 'default', 'via', '198.18.63.1'],
                        ['ip', 'addr', 'add', self.gateway + '/24', 'dev', self.iface],
                        ['sysctl', '-q', '-w', 'net.ipv4.ip_forward=1']):
            self.command(command, namespace=True)
        self.command(['ip', 'route', 'add', self.network, 'via', '198.18.63.2', 'dev', self.host_side])
        self.add_rule('filter', 'FORWARD', ['-i', self.host_side, '-o', host_uplink, '-s', self.network, '-j', 'ACCEPT'])
        self.add_rule('filter', 'FORWARD', ['-i', host_uplink, '-o', self.host_side, '-d', self.network, '-m', 'conntrack', '--ctstate', 'ESTABLISHED,RELATED', '-j', 'ACCEPT'])
        self.add_rule('nat', 'POSTROUTING', ['-s', self.network, '-o', host_uplink, '-j', 'MASQUERADE'])
        self.stage = 'fixture-AP'
        config = self.runtime / 'hostapd.conf'
        controls = self.runtime / 'control'
        psk = hashlib.pbkdf2_hmac('sha1', secret, ssid, 4096, 32).hex()
        config.write_text(f'interface={self.iface}\ndriver=nl80211\nssid2={ssid.hex()}\nhw_mode=g\nchannel=6\n'
                          f'ctrl_interface={controls}\nignore_broadcast_ssid=1\n'
                          f'wpa=2\nwpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\nwpa_psk={psk}\n')
        self.cli = ['hostapd_cli', '-p', str(controls), '-i', self.iface]
        self.ap = self.child('hostapd', ['hostapd', str(config)])
        deadline = time.monotonic() + 15
        while not (controls / self.iface).exists():
            require(self.ap.poll() is None and time.monotonic() < deadline, 'FIXTURE_AP_UNAVAILABLE')
            time.sleep(.2)
        self.wait_radio(True)
        self.stage = 'fixture-DHCP'
        resolver = self.payload.get('resolver', '1.1.1.1')
        require(resolver in ('1.1.1.1', '9.9.9.9'), 'FIXTURE_DNS_INVALID')
        self.dhcp = self.child('dnsmasq', ['dnsmasq', '--keep-in-foreground', '--conf-file=/dev/null',
            '--bind-interfaces', '--no-resolv', '--server=' + resolver + '@' + self.gateway, '--interface=' + self.iface,
            '--except-interface=lo', '--dhcp-authoritative', '--dhcp-range=10.247.63.21,10.247.63.24,255.255.255.0,2m',
            '--dhcp-leasefile=' + str(self.runtime / 'leases')])
        time.sleep(.4)
        require(self.dhcp.poll() is None, 'FIXTURE_DHCP_UNAVAILABLE')

    def wait_radio(self, on):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            result = self.command(self.cli + ['status'], namespace=True)
            if ('state=ENABLED' in result) == on:
                return
            time.sleep(.3)
        raise probe.ProbeError('FIXTURE_RADIO_NOT_IN_REQUESTED_STATE')

    def set_radio(self, on):
        require(self.command(self.cli + ['enable' if on else 'disable'], namespace=True) == 'OK', 'FIXTURE_RADIO_REJECTED')
        self.wait_radio(on)

    def block_internet(self):
        require(not self.internet_blocked, 'FIXTURE_INTERNET_ALREADY_BLOCKED')
        self.add_rule('filter', 'FORWARD', ['-i', self.host_side, '-s', self.network, '-j', 'DROP'])
        self.internet_blocked = True

    def require_host_unchanged(self):
        deadline = time.monotonic() + 5
        while True:
            current = self.host_snapshot()
            changed = snapshot_changes(self.before, current, self.profile['protectedInterfaces'])
            if not changed:
                return
            if time.monotonic() >= deadline:
                raise probe.ProbeError('HOST_NETWORK_CHANGED:' + ','.join(changed))
            time.sleep(.3)

    def cleanup(self):
        removed = True
        for child in reversed(self.children):
            if child.poll() is None:
                child.terminate()
                try:
                    child.wait(timeout=6)
                except subprocess.TimeoutExpired:
                    child.kill()
                    child.wait(timeout=3)
        for table, chain, args in reversed(self.rules):
            try:
                self.command(['iptables', '-w', '2', '-t', table, '-D', chain] + args)
            except Exception:
                removed = False
        if self.veth:
            try:
                self.command(['ip', 'link', 'delete', self.host_side])
            except Exception:
                removed = False
        return super().cleanup() and removed


def main():
    os.umask(0o077)
    fixture = UplinkFixture(probe.read_gate(30))
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP, signal.SIGALRM):
        signal.signal(sig, lambda *_: (_ for _ in ()).throw(probe.ProbeError('WATCHDOG_OR_TERMINATION')))
    signal.alarm(900)
    try:
        fixture.start()
        fixture.emit('prepared', hidden=True, dhcp=True)
        while True:
            action = probe.read_gate(600).get('radio')
            if action == 'done':
                break
            if action == 'block-internet':
                fixture.block_internet()
                fixture.wait_radio(True)
                fixture.emit('internet', state='blocked', radio='on')
                continue
            require(action in ('on', 'off'), 'FIXTURE_COMMAND_INVALID')
            fixture.set_radio(action == 'on')
            fixture.emit('radio', state=action)
    except Exception as error:
        fixture.emit('error', code=str(error) if isinstance(error, probe.ProbeError) else type(error).__name__, stage=fixture.stage)
    finally:
        signal.alarm(0)
        fixture.emit('complete', restored=fixture.cleanup(), cleanupErrors=fixture.cleanup_errors)


if __name__ == '__main__':
    main()
