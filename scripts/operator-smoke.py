#!/usr/bin/env python3
"""Qualify live logs, events, metrics, native shells and localhost forwarding."""
import argparse
import datetime
import fcntl
import json
import os
from pathlib import Path
import re
import select
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import time
import urllib.request
import urllib.parse

from terminal_screen import screen

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, default=Path('bin/kqt'))
parser.add_argument('--kubeconfig', type=Path)
args = parser.parse_args()

def setup_child():
    signal.pthread_sigmask(signal.SIG_SETMASK, [])
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)

def closed(url):
    address = urllib.parse.urlsplit(url)
    try:
        with socket.create_connection((address.hostname, address.port), timeout=0.5):
            return False
    except OSError:
        return True

def run(root, bad_shell=False):
    width, height = 80, 24
    config = root / ('bad-shell.yaml' if bad_shell else 'config.yaml')
    config.write_text('version: 1\nlayout: classic\ntheme: catppuccin-mocha\n'
                      + ('shell: /kqt-missing-shell\n' if bad_shell else ''))
    launch = [str(args.binary.resolve()), '--config', str(config), '--view', 'pods', '-n', 'kqt-test' if args.kubeconfig else 'default']
    launch += ['--kubeconfig', str(args.kubeconfig.resolve())] if args.kubeconfig else ['--demo']
    wrapper = "import subprocess,sys; rc=subprocess.call(sys.argv[1:]); print('__kqt_exit__'+str(rc),flush=True); sys.stdin.readline(); sys.exit(rc)"
    master, slave = os.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
    original = termios.tcgetattr(slave)
    env = dict(os.environ, TERM='xterm-256color', COLORTERM='truecolor', XDG_CACHE_HOME=str(root/'cache'))
    process = subprocess.Popen([sys.executable, '-c', wrapper, *launch], stdin=slave, stdout=slave, stderr=slave, env=env, preexec_fn=setup_child)
    raw = bytearray()
    resize_events = [(0, width, height)]
    def current_screen():
        return screen(raw, width, height, resize_events)
    def pump(duration=0.2):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], max(0, deadline-time.monotonic()))
            if ready:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                if not data:
                    break
                raw.extend(data)
    def send(data):
        os.write(master, data)
        pump(0.3)
    def expect(text, timeout=15):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            pump()
            view = current_screen()
            if text in view:
                return view
        raise AssertionError(text + ' not on screen:\n' + current_screen())
    def expect_port(number):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            pump()
            view = current_screen()
            match = re.search(r'Forward #' + str(number) + r'.*127\.0\.0\.1:(\d+)', view)
            if match:
                return 'http://127.0.0.1:' + match[1] + '/'
        raise AssertionError('forward did not start:\n' + view)
    try:
        expect('pods [5]' if args.kubeconfig else 'pods [12]')
        if not bad_shell:
            send(b':themes\r'); expect('Themes'); send(b'\x1b')
            send(b':layouts\r'); expect('Layouts'); send(b'\x1b')
            send(b':theme dr'); expect(':theme dr')
            send(b'\x1b[A'); expect(':layouts')
            send(b'\x1b[A'); expect(':themes')
            send(b'\x1b[B'); expect(':layouts')
            send(b'\x1b[B'); expect(':theme dr')
            send(b'\x1b[A\r'); expect('Layouts'); send(b'\x1b')
        selected = 'kqt-log-fixture' if args.kubeconfig else 'web0000'
        send(('/' + selected + '\r').encode()); expect('pods [1]')
        if bad_shell:
            send(b's'); expect('Shell failed:'); send(b'\x1b'); expect('pods [1]')
        else:
            send(b'S'); expect('Status & Events'); expect('Containers')
            expect('live')
            send(b'2'); expect('Labels ('); expect('[2: Labels]')
            if not args.kubeconfig: expect('app: web')
            send(b'\t'); expect('Annotations ('); expect('[3: Annotations]')
            send(b'\t'); expect('Finalizers ('); expect('[4: Finalizers]')
            send(b'\x1b[Z'); expect('Annotations (')
            destination = root / 'metadata.txt'
            send(b'o'); send(b'\x01\x0b' + str(destination).encode() + b'\r')
            expect('Metadata saved to')
            assert 'Annotations (' in destination.read_text() and 'Containers' not in destination.read_text(), 'metadata export lost the active tab'
            assert destination.stat().st_mode & 0o777 == 0o600, 'metadata export is not private'
            send(b'1'); expect('Containers')
            send(b'G'); expect('kqt-event-marker' if args.kubeconfig else 'Back-off restarting')
            if not args.kubeconfig:
                expect('demo event heartbeat')
                send(b' '); expect('paused')
                before = current_screen().splitlines()[4:-2]
                pump(2)
                assert before == current_screen().splitlines()[4:-2], 'paused events moved'
                send(b' ')
                send(b'/no-such-event\r'); expect('No events match')
                send(b'/\x01\x0bdemo event heartbeat\r'); expect('demo event heartbeat')
                destination = root / 'selected-events.log'
                send(b'o'); send(b'\x01\x0b' + str(destination).encode() + b'\r')
                expect('Events saved to')
                assert 'demo event heartbeat' in destination.read_text(), 'event export lost displayed lines'
                assert destination.stat().st_mode & 0o777 == 0o600, 'event export is not private'
            send(b'g'); expect('Pod IP'); send(b'\x1b')
            send(b'M'); expect('Labels ('); send(b'\x12'); expect('Labels ('); send(b'\x1b')
            send(b':metadata finalizers\r'); expect('Finalizers ('); send(b'\x1b')
            send(b'm'); expect('CPU'); expect('MEMORY'); send(b'm')
            send(b'L')
            if not args.kubeconfig:
                expect('Containers'); send(b'\r')
            marker = 'kqt-live-log-marker' if args.kubeconfig else 'live heartbeat'
            expect(marker); send(b' '); expect('paused')
            before = [line for line in current_screen().splitlines() if marker in line]
            pump(2)
            after = [line for line in current_screen().splitlines() if marker in line]
            assert before == after, 'paused logs moved'
            send(b'/no-such-log-line\r'); expect('No log lines match')
            send(('/\x01\x0b' + marker + '\r').encode()); expect(marker)
            send(b' '); expect(marker)
            destination = root / 'saved.log'
            send(b'o'); send(b'\x01\x0b' + str(destination).encode() + b'\r')
            deadline = time.monotonic() + 5
            while not destination.exists() and time.monotonic() < deadline:
                pump()
            assert marker in destination.read_text(), 'log export lost displayed lines'
            assert destination.stat().st_mode & 0o777 == 0o600, 'export is not private'
            send(b'o'); send(b'\x01\x0b' + str(destination).encode() + b'\r'); expect('Unable to save logs')
            send(b'F'); expect('snapshot'); send(b'F'); expect('live'); send(b'\x1b')
            if args.kubeconfig:
                send(b's'); expect('kqt shell')
                send(b'printf "kqt-shell-%s\\n" marker; stty size\r'); expect('kqt-shell-marker'); expect('24 80')
                width, height = 132, 36
                resize_events.append((len(raw), width, height))
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                os.killpg(process.pid, signal.SIGWINCH); pump(1)
                send(b'stty size\r'); expect('36 132')
                send(b'sleep 30\r'); send(b'\x03'); send(b'printf "kqt-shell-%s\\n" after-interrupt\r'); expect('kqt-shell-after-interrupt')
                width, height = 80, 24
                resize_events.append((len(raw), width, height))
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                os.killpg(process.pid, signal.SIGWINCH); pump(0.5)
                send(b'exit\r'); expect('Shell closed'); expect('pods [1]')
                send(b's'); expect('kqt shell'); send(b'\x1d'); expect('Shell closed'); expect('pods [1]')
                send(b':forward 0:8080\r'); first = expect_port(1)
                with urllib.request.urlopen(first, timeout=3) as response:
                    assert b'kqt-forward-marker' in response.read(), 'wrong forwarded traffic'
                send(b'F'); expect('Forwards'); send(b'\r'); expect('Port forward #1'); expect('Local: 127.0.0.1:')
                send(b'x'); expect('stopped'); send(b'\x1b')
                assert closed(first), 'stopped forward still accepts traffic'
                send(b':forward 0:8080\r'); second = expect_port(2)
                send(b'x'); expect('Port forward #2 stopped')
                assert closed(second), 'resource-list x left the forwarding listener open'
                send(b':forward 0:8080\r'); third = expect_port(3)
                send(b'F'); expect('Forwards')
                # Filter to the running forward so x exercises the list directly.
                send(b'active'); send(b'x'); expect('No matches')
                expect('Port forward #3 stopped')
                assert closed(third), 'forwards-list x left the forwarding listener open'
                send(b'\x1b')
                send(b':svc\r'); expect('services [2]')
                send(b'/kqt-http\r'); expect('services [1]')
                send(b':forward 0:80\r'); service = expect_port(4)
                with urllib.request.urlopen(service, timeout=3) as response:
                    assert b'kqt-forward-marker' in response.read(), 'wrong service-forwarded traffic'
                send(b'x'); expect('Port forward #4 stopped')
                assert closed(service), 'selected service x left the resolved pod forward open'
                send(b':pods\r'); expect('pods [5]')
                send(b'/kqt-log-fixture\r'); expect('pods [1]')
                send(b':forward 0:8080\r'); second = expect_port(5)
            else:
                send(b's'); expect('live Kubernetes connection')
            send(b':events\r'); expect('Events · live')
            if args.kubeconfig:
                context = subprocess.check_output(['kubectl', '--kubeconfig', str(args.kubeconfig.resolve()), 'config', 'current-context'], text=True).strip()
                assert context.startswith('k3d-kqt-smoke-'), 'event mutation requires disposable fixture'
                width, height = 140, 30
                resize_events.append((len(raw), width, height))
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                os.killpg(process.pid, signal.SIGWINCH); pump(0.5)
                send(b'/kqt-event-marker\r'); expect('events [1]'); expect('kqt-event-marker')
                send(b' '); expect('paused')
                before = current_screen().splitlines()[4:-2]
                patch = json.dumps({'count': 7, 'lastTimestamp': datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00', 'Z')})
                subprocess.run(['kubectl', '--kubeconfig', str(args.kubeconfig.resolve()), 'patch', 'event', 'kqt-fixture-event', '-n', 'kqt-test', '--type=merge', '-p', patch], check=True, stdout=subprocess.DEVNULL)
                pump(1)
                assert before == current_screen().splitlines()[4:-2], 'paused live event table moved'
                send(b' ')
                deadline = time.monotonic() + 5
                while not re.search(r'KqtFixture\s+7\s+', current_screen()) and time.monotonic() < deadline:
                    pump()
                assert re.search(r'KqtFixture\s+7\s+', current_screen()), 'live event repeat update did not reach table'
                destination = root / 'live-events.log'
                send(b'o'); send(b'\x01\x0b' + str(destination).encode() + b'\r'); expect('Events saved to')
                assert '×7 kqt-event-marker' in destination.read_text(), 'live event export missed repeat update'
            else:
                # At 80 columns the last columns scroll horizontally.
                send(b'/DemoHeartbeat\r'); expect('events [1]')
                send(b' '); expect('paused')
                before = current_screen().splitlines()[4:-2]
                pump(2)
                assert before == current_screen().splitlines()[4:-2], 'paused event table moved'
                send(b' '); expect('following')
                send(b'0'); expect('Namespace: all')
                send(b'/DemoHeartbeat\r'); expect('events [2]')
                destination = root / 'all-events.log'
                send(b'o'); send(b'\x01\x0b' + str(destination).encode() + b'\r'); expect('Events saved to')
                assert '[default/Pod/' in destination.read_text() and '[platform/Pod/' in destination.read_text(), 'all-namespace export lost scope'
                send(b':ns default\r'); send(b'/DemoHeartbeat\r'); expect('events [1]')
        send(b'q')
        deadline = time.monotonic() + 5
        while b'__kqt_exit__0' not in raw and time.monotonic() < deadline:
            pump()
        assert b'__kqt_exit__0' in raw, 'kqt did not exit normally'
        assert original == termios.tcgetattr(slave), 'terminal attributes were not restored'
        if args.kubeconfig and not bad_shell:
            assert closed(second), 'quitting left a port forward open'
        send(b'\n'); process.wait(timeout=5)
        print('PASS operator PTY ' + ('missing-shell recovery' if bad_shell else 'live history/logs/events/metrics/shell/forward' if args.kubeconfig else 'demo history/logs/events/metrics'))
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL); process.wait()
        os.close(master); os.close(slave)

with tempfile.TemporaryDirectory(prefix='kqt-operator-') as temporary:
    run(Path(temporary))
    if args.kubeconfig:
        run(Path(temporary), bad_shell=True)
