# SPDX-License-Identifier: MIT
"""Egress audit for no-spend benches: no request may leave the machine.

Only task-completion benches may use a live API key. Every other bench points each CLI at the local mock endpoint with
a dummy key value and an isolated HOME that holds no credentials. This module checks that nothing else leaves the
host in two ways:

  isolated network namespace   when the bench runs with no non-loopback interface or no default route (a container
                               with `--network none`, or `unshare --net`), egress is impossible by construction.
  socket audit (Linux)         while a harness runs, its process tree's sockets are read from /proc every 25 ms; any
                               connected socket whose remote address is not loopback is a violation. A connection
                               that opens and closes inside one sampling interval can be missed, which is why the
                               report states the interval, and why a strict run should use an isolated namespace.

A violation fails the sample and the bench (non-zero exit).
"""
import ipaddress
import os

NET_FILES = ("tcp", "tcp6", "udp", "udp6")
SAMPLE_INTERVAL_S = 0.025


def _hex_ip(hexa):
    raw = bytes.fromhex(hexa)
    if len(raw) == 4:
        return str(ipaddress.IPv4Address(raw[::-1]))
    words = [raw[i:i + 4][::-1] for i in range(0, 16, 4)]
    return str(ipaddress.IPv6Address(b"".join(words)))


def parse_proc_net(text):
    """Return {inode: (remote_ip, remote_port)} for connected sockets in a /proc/net/{tcp,udp}[6] dump (remote 0 means not connected)."""
    out = {}
    for line in text.splitlines()[1:]:
        f = line.split()
        if len(f) < 10:
            continue
        try:
            ip, port = f[2].split(":")
            remote = _hex_ip(ip)
            out[int(f[9])] = (remote, int(port, 16))
        except (ValueError, IndexError):
            continue
    return out


def is_local(ip):
    addr = ipaddress.ip_address(ip)
    if addr.is_unspecified:
        return True
    if isinstance(addr, ipaddress.IPv6Address) and addr.ipv4_mapped:
        addr = addr.ipv4_mapped
    return addr.is_loopback


def socket_inodes(pid):
    inodes = set()
    try:
        for fd in os.listdir(f"/proc/{pid}/fd"):
            try:
                target = os.readlink(f"/proc/{pid}/fd/{fd}")
            except OSError:
                continue
            if target.startswith("socket:["):
                inodes.add(int(target[8:-1]))
    except OSError:
        pass
    return inodes


def _read_net(name):
    with open(f"/proc/net/{name}") as f:
        return f.read()


def scan(pids, read=None):
    """Return the set of 'ip:port' strings that member processes are connected to outside the loopback."""
    read = read or _read_net
    inodes = set()
    for pid in pids:
        inodes |= socket_inodes(pid)
    if not inodes:
        return set()
    found = set()
    for name in NET_FILES:
        try:
            table = parse_proc_net(read(name))
        except OSError:
            continue
        for inode in inodes:
            if inode in table and not is_local(table[inode][0]):
                found.add(f"{table[inode][0]}:{table[inode][1]}")
    return found


def netns_isolated():
    """True when this network namespace has no interface besides loopback and no route: nothing can leave."""
    try:
        with open("/proc/net/route") as f:
            routes = [ln for ln in f.read().splitlines()[1:] if ln.strip()]
        with open("/proc/net/dev") as f:
            ifaces = [ln.split(":")[0].strip() for ln in f.read().splitlines()[2:] if ":" in ln]
    except OSError:
        return False
    return not routes and all(i == "lo" for i in ifaces)


def describe():
    """How this run is protected, for the report."""
    if netns_isolated():
        return {"mode": "isolated-network-namespace", "note": "no non-loopback interface and no route: egress is impossible"}
    if os.path.isdir("/proc/net"):
        return {"mode": "socket-audit", "interval_ms": int(SAMPLE_INTERVAL_S * 1000), "note": "connected non-loopback sockets of each harness tree are sampled; a connection shorter than the interval can be missed"}
    return {"mode": "none", "note": "no socket audit on this platform; dummy keys and an isolated HOME only"}


OBSERVED = set()  # remotes seen by meters that have been closed since the last take()


def take():
    """Return and clear the non-loopback remotes observed since the last call."""
    seen = set(OBSERVED)
    OBSERVED.clear()
    return seen
