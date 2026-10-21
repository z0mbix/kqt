#!/usr/bin/env python3
"""Exercise kqt in a PTY and verify navigation and terminal restoration."""
import argparse
import json
import shlex
import fcntl
import os
from pathlib import Path
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

from terminal_screen import screen

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, default=Path('bin/kqt'))
parser.add_argument('--snapshots', type=Path)
parser.add_argument('--kubeconfig', type=Path, help='Test a disposable live fixture instead of demo')
args = parser.parse_args()
binary = str(args.binary.resolve())

def has_text_colour(data, text, colour):
    # The terminal renderer may merge the key's colour with the preceding
    # border. Follow SGR state rather than requiring a new escape at the key.
    foreground, end = None, 0
    def contains_text(chunk):
        return text in re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]', b'', chunk)
    for match in re.finditer(rb'\x1b\[([0-9;]*)m', data):
        if foreground == colour and contains_text(data[end:match.start()]):
            return True
        codes = [int(n or 0) for n in match.group(1).split(b';')]
        i = 0
        while i < len(codes):
            code = codes[i]
            if code in (0, 39): foreground = None
            elif 30 <= code <= 37 or 90 <= code <= 97: foreground = code
            elif code in (38, 48) and i + 1 < len(codes):
                if codes[i+1] == 2 and i + 4 < len(codes):
                    if code == 38: foreground = tuple(codes[i+2:i+5])
                    i += 5
                    continue
                if codes[i+1] == 5 and i + 2 < len(codes):
                    if code == 38: foreground = codes[i+2]
                    i += 3
                    continue
            i += 1
        end = match.end()
    return foreground == colour and contains_text(data[end:])

def setup_child():
    signal.pthread_sigmask(signal.SIG_SETMASK, [])
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)

def check_restart(config, width, height, env, wrapper):
    master, slave = os.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
    original = termios.tcgetattr(slave)
    launch = [binary, '--config', str(config)]
    launch += ['--kubeconfig', str(args.kubeconfig.resolve())] if args.kubeconfig else ['--demo']
    process = subprocess.Popen([sys.executable, '-c', wrapper, *launch], stdin=slave, stdout=slave, stderr=slave, preexec_fn=setup_child, env=env)
    raw = bytearray()
    def pump(duration=0.15):
        deadline=time.monotonic()+duration
        while time.monotonic()<deadline:
            ready,_,_=select.select([master],[],[],max(0,deadline-time.monotonic()))
            if ready:
                try: data=os.read(master,65536)
                except OSError: break
                if not data: break
                raw.extend(data)
    try:
        expected = 'pods [5]' if args.kubeconfig else 'statefulsets.apps [1]'
        namespace = 'kqt-test' if args.kubeconfig else 'platform'
        deadline=time.monotonic()+20
        while time.monotonic()<deadline:
            pump()
            current=screen(raw,width,height)
            if expected in current and 'connected' in current: break
        else: raise AssertionError('saved view not restored:\n'+current)
        assert 'Namespace: '+namespace in current, 'saved namespace not restored'
        os.write(master,b':layouts\r');pump(0.4)
        os.write(master,b'smoke-saved');pump(0.3)
        current=screen(raw,width,height)
        assert 'smoke-saved' in current and 'active' in current, 'saved layout not restored'
        os.write(master,b'\x1b');pump(0.4)
        # Observe the ten-second startup announcement disappearing while the
        # connection remains stable throughout repeated background refreshes.
        if width==80 and height==24:
            deadline=time.monotonic()+11
            while time.monotonic()<deadline:
                pump(0.3)
                line=screen(raw,width,height).splitlines()[0]
                assert 'connected' in line and 'loading' not in line, 'refresh flickered'
            assert 'nord' not in line, 'theme announcement did not expire'
        os.write(master,b'q')
        deadline=time.monotonic()+5
        while b'__EXIT_RC__=0' not in raw and time.monotonic()<deadline: pump()
        assert b'__EXIT_RC__=0' in raw, 'restarted app did not exit cleanly'
        assert termios.tcgetattr(slave)==original, 'restarted app did not restore terminal'
        os.write(master,b'\n');pump(0.3);process.wait(timeout=5)
        print(f'PASS: {width}x{height}: saved view/namespace/layout restart and stable refresh')
    finally:
        if process.poll() is None:
            os.killpg(process.pid,signal.SIGKILL);process.wait()
        os.close(master);os.close(slave)

with tempfile.TemporaryDirectory(prefix='kqt-tui-') as directory:
    config = Path(directory) / 'config.yaml'
    editor = Path(directory) / 'editor-fixture.py'
    editor.write_text(r"""import pathlib,re,sys,time
path=pathlib.Path(sys.argv[-1])
text=path.read_text()
value='tty-'+str(time.monotonic_ns())
entry='    kqt.dev/editor-tested: '+value+'\n'
if '    kqt.dev/editor-tested:' in text:
    text=re.sub(r'    kqt.dev/editor-tested:.*\n',entry,text,count=1)
elif '  annotations:\n' in text:
    text=text.replace('  annotations:\n','  annotations:\n'+entry,1)
else:
    text=text.replace('metadata:\n','metadata:\n  annotations:\n'+entry,1)
path.write_text(text)
print('__KQT_EDITOR_STARTED__',flush=True)
""")
    for width, height, layout in [(80, 24, 'classic'), (180, 48, 'inspector'), (80, 40, 'bottom')]:
        editor_command = shlex.quote(sys.executable)+' '+shlex.quote(str(editor))
        config.write_text('version: 1\ntheme: catppuccin-mocha\nrefresh_interval: 1s\neditor: '+json.dumps(editor_command)+'\n')
        master, slave = os.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
        original = termios.tcgetattr(slave)
        env = dict(os.environ, TERM='xterm-256color', COLORTERM='truecolor', KQT_THEME='', XDG_CACHE_HOME=str(Path(directory)/'cache'))
        env.pop('NO_COLOR', None)
        wrapper = '''import subprocess,sys,signal
child = subprocess.Popen(sys.argv[1:])
signal.signal(signal.SIGTERM, lambda *_: None)
code = child.wait()
print('__EXIT_RC__='+str(code), flush=True)
sys.stdin.readline()
sys.exit(code)
'''
        launch = [binary, '--config', str(config), '--layout', layout]
        if args.kubeconfig:
            launch += ['--kubeconfig', str(args.kubeconfig.resolve()), '--namespace', 'kqt-test']
        else:
            launch += ['--demo']
        process = subprocess.Popen([sys.executable, '-c', wrapper, *launch], stdin=slave, stdout=slave, stderr=slave, preexec_fn=setup_child, env=env)
        raw = bytearray()
        def pump(duration=0.15):
            deadline = time.monotonic() + duration
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], max(0, deadline-time.monotonic()))
                if ready:
                    try: chunk = os.read(master, 65536)
                    except OSError: break
                    if not chunk: break
                    raw.extend(chunk)
        def expect(text):
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                pump()
                current = screen(raw, width, height)
                if text in current: return current
            raise AssertionError(text + ' not on screen:\n' + screen(raw, width, height))
        def send(data):
            os.write(master, data)
            pump(0.25)
        def snapshot(name):
            if args.snapshots:
                args.snapshots.mkdir(parents=True, exist_ok=True)
                prefix = args.snapshots / f'{width}x{height}-{name}'
                prefix.with_suffix('.txt').write_text(screen(raw, width, height))
                prefix.with_suffix('.ansi').write_bytes(raw)
        def expect_yaml_highlighting(start):
            # Assert that the key's active-theme colour surrounds the YAML
            # text itself; coloured borders alone do not qualify highlighting.
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                pump()
                if has_text_colour(bytes(raw[start:]), b'apiVersion', (203, 166, 247)):
                    return
            raise AssertionError('YAML key did not use Catppuccin Mocha syntax colour')
        def check_secret_toggle(marker):
            send(b'x'); expect(marker); expect('values visible')
            send(b'J'); expect('JSON'); expect(marker)
            send(b'x'); expect('redacted')
            assert marker not in screen(raw, width, height), 'x did not hide decoded values'
            send(b'x'); expect(marker)
            send(b'\x1b'); pump(0.4)
            assert marker not in screen(raw, width, height), 'closing details retained decoded values'
        try:
            expected = 'pods [5]' if args.kubeconfig else 'pods [12]'
            current = expect(expected)
            expect('Explorer')
            expect('kqt-test' if args.kubeconfig else 'web-7c9f6d8b5-0000')
            snapshot('pods')
            assert b'38;2;' in raw or b'38;5;' in raw, 'theme colors absent'
            expect('j/k: move')
            assert has_text_colour(bytes(raw), b'j/k', (203, 166, 247)), 'shortcut key did not use theme accent'
            assert has_text_colour(bytes(raw), b': move', (166, 173, 200)), 'shortcut description did not retain muted colour'
            # SGR mouse press/release: a release must not toggle order again.
            header = screen(raw, width, height).splitlines()[2]
            column_x = header.index('NAME') + 1
            send(f'\x1b[<0;{column_x};3M\x1b[<0;{column_x};3m'.encode())
            assert 'NAME ↓' in screen(raw, width, height).splitlines()[2], 'header click did not sort descending'
            send(b'R')
            assert 'NAME ↑' in screen(raw, width, height).splitlines()[2], 'R did not reverse'
            send(b'O')
            assert 'READY ↑' in screen(raw, width, height).splitlines()[2], 'O did not cycle to the next column'
            send(b'\x1b[1;2C') # Shift+Right sorts next visible column.
            assert 'STATUS ↑' in screen(raw, width, height).splitlines()[2], 'keyboard column sort failed'
            send(b'\x1b[1;2D'); send(b'\x1b[1;2D')
            assert 'NAME ↑' in screen(raw, width, height).splitlines()[2], 'keyboard name sort failed'
            send(b':sort\r'); expect('Sort'); send(b'restarts\r')
            assert 'RESTARTS' in screen(raw, width, height).splitlines()[2], 'sort picker failed'
            send(b':sort name asc\r')
            assert b'\x1b[?1002h' in raw, 'mouse reporting was not enabled'
            if args.kubeconfig:
                send(b'/kqt-log-fixture\r'); expect('pods [1]'); send(b'e'); expect('Review changes'); send(b'\r'); expect('Updated Pod'); send(b'y'); expect('YAML'); expect('kqt-log-fixture'); expect('kqt.dev/editor-tested'); snapshot('yaml')
                send(b'\x1b'); pump(0.4)
                send(b'L'); expect('Logs'); expect('kqt-live-log-marker'); snapshot('logs')
                send(b'\x1b'); pump(0.4)
                send(b':deploy\r'); expect('kqt-deployment'); send(b'L'); expect('kqt-deployment-log-marker'); snapshot('deployment-logs'); send(b'\x1b'); pump(0.4)
                send(b':sts\r'); expect('kqt-stateful'); send(b'L'); expect('kqt-stateful-0'); expect('kqt-stateful-1'); snapshot('stateful-logs'); send(b'\x1b'); pump(0.4)
                send(b':widgets\r'); expect('live-widget'); expect('Ready')
                send(b':secrets\r'); expect('fixture-secret')
                send(b'y'); expect('<redacted>')
                assert 'LIVE_SECRET_MUST_NOT_APPEAR' not in screen(raw, width, height)
                check_secret_toggle('LIVE_SECRET_MUST_NOT_APPEAR')
                send(b':pods\r'); expect('pods [5]')
            else:
                send(b'j'); expect('› web-7c9f6d8b5-0001')
                send(b'k'); expect('› web-7c9f6d8b5-0000')
                send(b'/0004\r'); expect('pods [1]'); expect('CrashLoopBackOff')
                send(b'\x1b'); pump(0.4); expect('pods [12]')
                send(b'/web0003\r'); expect('pods [1]'); expect('web-7c9f6d8b5-0003')
                send(b'\x1b'); pump(0.4); expect('pods [12]'); send(b'g')
                send(b'S'); expect('Status & Events'); expect('Containers')
                send(b'G'); expect('Back-off restarting'); send(b'g'); expect('Pod IP')
                send(b'\x12'); expect('Containers'); send(b'\x1b'); pump(0.4)
                yaml_start = len(raw)
                send(b'y'); expect('YAML'); expect_yaml_highlighting(yaml_start)
                send(b'\x04'); expect('nginx:1.28'); snapshot('yaml')
                send(b'G'); expect('restartCount'); send(b'g')
                send(b'\x1b'); pump(0.4)
                send(b'L'); expect('Containers'); send(b'\r'); expect('Logs'); expect('listening on :80'); snapshot('logs')
                send(b'p'); expect('· previous')
                send(b'\x1b'); pump(0.4)
                send(b'n'); expect('namespaces [3]'); send(b'\r'); expect('Namespace: default'); expect('pods [12]')
                send(b'0'); expect('Namespace: all'); expect('pods [26]'); expect('NAMESPACE')
                send(b':ns platform\r'); expect('Namespace: platform'); expect('gateway-7c9f6d8b5')
                send(b':deploy\r'); expect('deployments.apps [1]'); send(b'L'); expect('Logs'); expect('gateway-7c9f6d8b5-0012'); send(b'\x1b'); pump(0.4); send(b'\r'); expect('pods [12]'); expect('app=gateway')
                send(b'\x1b'); pump(0.4); expect('deployments.apps [1]')
                send(b':sts\r'); expect('statefulsets.apps [1]'); send(b'L'); expect('redis-0'); expect('redis-1'); send(b'\x1b'); pump(0.4)
                send(b':widgets\r'); expect('example'); expect('Ready')
                send(b':ns default\r:secrets\r'); expect('demo-credentials'); send(b'y'); expect('<redacted>')
                assert 'ZGVtby1vbmx5' not in screen(raw, width, height)
                check_secret_toggle('demo-only')
                send(b':pods\r'); expect('pods [12]')
                if layout != 'classic':
                    send(b'\t'); expect('Inspector · focused'); send(b'G'); expect('100%'); send(b'J'); expect('focused · JSON'); send(b'y'); expect('focused · YAML')
                    send(b'\x1b'); pump(0.4)
            send(b':the\t'); expect(':theme'); send(b'\t'); expect(':themes'); send(b'\x1b'); pump(0.4)
            if not args.kubeconfig:
                send(b'g'); send(b'e'); expect('Review changes'); send(b'\r'); expect('Updated Pod'); send(b'y'); expect('kqt.dev/editor-tested'); send(b'\x1b'); pump(0.4)
            send(b'J'); expect('JSON'); expect('"apiVersion"'); snapshot('json'); send(b'y'); expect('YAML'); send(b'\x1b'); pump(0.4)
            send(b'tnord'); expect('nord'); send(b'\r'); expect('· nord')
            for help_command in [b'?', b':help\r']:
                help_start = len(raw)
                send(help_command); expect('Help'); expect('Navigation')
                assert re.search(r'j/k, arrows: +Move through resources', screen(raw, width, height)), 'help shortcut alignment changed'
                assert has_text_colour(bytes(raw[help_start:]), b'j/k, arrows', (136, 192, 208)), 'help key did not use Nord accent'
                assert has_text_colour(bytes(raw[help_start:]), b'Move through resources', (216, 222, 233)), 'help description did not use Nord muted colour'
                snapshot('help'); send(b'\x1b'); pump(0.4)
            send(b':columns name,status,age\r:write smoke-saved\r'); expect('Saved layout smoke-saved')
            send(b':layouts\r'); expect('Layouts'); send(b'smoke-saved\r')
            if not args.kubeconfig:
                send(b':ns platform\r:sts\r'); expect('redis')
            send(b'q')
            deadline = time.monotonic() + 5
            while b'__EXIT_RC__=0' not in raw and time.monotonic() < deadline: pump()
            assert b'__EXIT_RC__=0' in raw, 'kqt did not exit cleanly'
            assert b'\x1b[?1049l' in raw, 'alternate screen not restored'
            assert termios.tcgetattr(slave) == original, 'terminal attributes not restored'
            send(b'\n'); process.wait(timeout=5)
            assert process.returncode == 0
            print(f'PASS: {width}x{height} {layout}: completion, fuzzy search, manifests, logs, editor, themes, terminal restoration', flush=True)
            check_restart(config,width,height,env,wrapper)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            os.close(master); os.close(slave)
