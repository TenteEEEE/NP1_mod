#!/usr/bin/env python3
"""Enter the dedicated NSS-independent UID before executing the Web server."""

import ctypes
import os
import sys


class CapHeader(ctypes.Structure):
    _fields_ = [('version', ctypes.c_uint32), ('pid', ctypes.c_int)]


class CapData(ctypes.Structure):
    _fields_ = [(name, ctypes.c_uint32) for name in ('effective', 'permitted', 'inheritable')]


libc = ctypes.CDLL(None, use_errno=True)
PR_SET_KEEPCAPS = 8
PR_CAPBSET_DROP = 24
PR_CAP_AMBIENT = 47
PR_CAP_AMBIENT_RAISE = 2
CAP_NET_BIND_SERVICE = 10
LINUX_CAPABILITY_VERSION_3 = 0x20080522
WEB = '/run/np1-control/ncc'


def checked(result):
    if result != 0:
        error = ctypes.get_errno()
        raise OSError(error, os.strerror(error))


uid = int(sys.argv[1])
if not 60000 <= uid < 61184 or os.geteuid() != 0:
    raise SystemExit('Expected root and the dedicated non-system UID')

with open('/proc/sys/kernel/cap_last_cap', encoding='ascii') as limit:
    for cap in range(int(limit.read()) + 1):
        if cap != CAP_NET_BIND_SERVICE:
            checked(libc.prctl(PR_CAPBSET_DROP, cap, 0, 0, 0))

os.setgroups([])
os.setresgid(uid, uid, uid)
checked(libc.prctl(PR_SET_KEEPCAPS, 1, 0, 0, 0))
os.setresuid(uid, uid, uid)
checked(libc.prctl(PR_SET_KEEPCAPS, 0, 0, 0, 0))

header = CapHeader(LINUX_CAPABILITY_VERSION_3, 0)
data = (CapData * 2)()
bind = 1 << CAP_NET_BIND_SERVICE
data[0] = CapData(bind, bind, bind)
checked(libc.capset(ctypes.byref(header), data))
checked(libc.prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE, CAP_NET_BIND_SERVICE, 0, 0))
os.execv(WEB, [WEB, '--mode', 'server'])
