#!/usr/bin/env python3
"""Exercise feature workflows through actual terminal input."""
import argparse
import base64
import json
import re
import fcntl
import os
from pathlib import Path
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

from terminal_screen import screen

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary',type=Path,default=Path('bin/kqt'))
parser.add_argument('--feature',required=True)
parser.add_argument('--kubeconfig',type=Path)
args=parser.parse_args()

def setup_child():
    signal.pthread_sigmask(signal.SIG_SETMASK, [])
    os.setsid()
    fcntl.ioctl(0,termios.TIOCSCTTY,0)

with tempfile.TemporaryDirectory(prefix='kqt-features-') as temporary:
    root=Path(temporary)
    env=dict(os.environ,TERM='xterm-256color',COLORTERM='truecolor',XDG_CACHE_HOME=str(root/'cache'))
    history_file=root/'cache'/'kqt'/'history.json'
    def history():
        return json.loads(history_file.read_text()) if history_file.exists() else []
    config=root/'config.yaml'
    config.write_text('version: 1\ntheme: catppuccin-mocha\nlayout: classic\n')
    if args.feature=='command-history':
        config.write_text(config.read_text()+'command_history: [layouts]\nhistory_limit: 5\n')
    if args.feature=='debug':
        assert not args.kubeconfig, 'debug PTY workflow uses only the simulated backend'
        config.write_text(config.read_text()+'''debug:
  default_profile: approved
  profiles:
    approved:
      image: registry.kqt.invalid/debug:approved
      security_context:
        runAsUser: 1000
''')
    master,slave=os.openpty()
    width,height=120,32
    fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
    original=termios.tcgetattr(slave)
    wrapper="import subprocess,sys; rc=subprocess.call(sys.argv[1:]); print('__kqt_exit__'+str(rc),flush=True); sys.stdin.readline(); sys.exit(rc)"
    launch=[str(args.binary.resolve()),'--config',str(config),'-n','kqt-test' if args.kubeconfig else 'default']
    launch += ['--kubeconfig',str(args.kubeconfig.resolve())] if args.kubeconfig else ['--demo']
    if args.feature=='access': launch += ['--read-only']
    process=subprocess.Popen([sys.executable,'-c',wrapper,*launch],stdin=slave,stdout=slave,stderr=slave,preexec_fn=setup_child,env=env)
    raw=bytearray()
    resize_events=[(0,width,height)]
    def pump(duration=0.15):
        end=time.monotonic()+duration
        while time.monotonic()<end:
            ready,_,_=select.select([master],[],[],max(0,end-time.monotonic()))
            if ready:
                try: data=os.read(master,65536)
                except OSError: break
                if not data: break
                raw.extend(data)
    def send(value):
        os.write(master,value.encode() if isinstance(value,str) else value)
        pump(0.2)
    def expect(value,timeout=10):
        end=time.monotonic()+timeout
        while time.monotonic()<end:
            pump()
            current=screen(raw,width,height,resize_events)
            if value in current: return current
        raise AssertionError(value+' not on screen:\n'+current)
    try:
        expect('pods [5]' if args.kubeconfig else 'pods [12]')
        if args.feature=='namespace-scopes':
            assert not args.kubeconfig, 'namespace workflow uses the isolated demo backend'
            send(':ns default, platform,default\r'); expect('pods [26]'); expect('Namespaces: 2 selected'); expect('NAMESPACE')
            send(':ns save production\r'); expect('Saved namespace group production'); expect('Namespaces: production (2)')
            text=config.read_text()
            assert 'namespace: default,platform' in text and 'namespace_groups:' in text and 'production:' in text
            send(':ns default\r'); expect('pods [12]')
            assert 'NAMESPACE' not in screen(raw,width,height,resize_events), 'single scope retained namespace column'
            send(':ns @pro\t'); expect(':ns @production'); send('\r'); expect('pods [26]')
            before=config.read_text()
            send(':ns default,\r'); expect('Invalid namespace selection')
            assert config.read_text()==before, 'invalid namespace selection changed config/history'
            send(':columns name,status\r'); expect('NAMESPACE')
            send(':nodes\r'); expect('nodes [3]'); send('\r'); expect('Namespaces: production (2)')
            send(':pods\r'); expect('pods [26]'); send('m'); expect('CPU'); expect('Mi')
            send(':events\r'); expect('DemoHeartbeat')
            end=time.monotonic()+5
            while time.monotonic()<end:
                current=screen(raw,width,height,resize_events)
                if 'platform' in current and 'default' in current: break
                pump()
            assert 'platform' in current and 'default' in current, 'combined live events lost a namespace'
            target=root/'events.log'
            send('o'); expect('Save to:'); send('\x15'+str(target)+'\r'); expect('Events saved to')
            exported=target.read_text()
            assert '[default/Pod/' in exported and '[platform/Pod/' in exported, 'event export omitted a selected namespace'
            send(':pods\r'); expect('pods [26]')
            send('q')
            end=time.monotonic()+5
            while b'__kqt_exit__0' not in raw and time.monotonic()<end: pump()
            assert b'__kqt_exit__0' in raw and original==termios.tcgetattr(slave), 'first session did not restore terminal'
            send('\n'); process.wait(timeout=5)
            os.close(master); os.close(slave)
            master,slave=os.openpty()
            fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
            original=termios.tcgetattr(slave)
            raw.clear(); resize_events=[(0,width,height)]
            # Omit -n on restart so saved scope is exercised.
            launch=[str(args.binary.resolve()),'--config',str(config),'--demo']
            process=subprocess.Popen([sys.executable,'-c',wrapper,*launch],stdin=slave,stdout=slave,stderr=slave,preexec_fn=setup_child,env=env)
            expect('pods [26]'); expect('Namespaces: production (2)')
            send(':ns default\r'); expect('pods [12]'); send(':ns @production\r'); expect('pods [26]')
        elif args.feature=='debug-setup':
            assert not args.kubeconfig, 'debug setup uses only the simulated backend'
            before=config.read_text()
            send('B'); expect('Debug image · first use'); expect('Use my own image'); expect('Use BusyBox')
            send('\x1b'); expect('pods [12]')
            assert config.read_text()==before, 'cancelled image choice was saved'
            send(':debug\r'); expect('Debug image · first use'); send('\r'); expect('Which image should kqt use')
            send('busybox\r'); expect('fully qualified')
            assert config.read_text()==before, 'invalid image was saved'
            image='123456789012.dkr.ecr.eu-west-2.amazonaws.com/platform/debug-tools:approved'
            send('\x01\x0b'+image+'\r'); expect('Start debug container'); expect('Enter to start')
            text=config.read_text()
            assert image in text and 'default_profile: default' in text, 'debug image was not saved'
            assert not history(), 'setup prematurely saved the debug command'
            send('\x1b'); send('B'); expect('Start debug container')
            assert 'Which image should kqt use' not in screen(raw,width,height,resize_events), 'saved image was not reused'
            send('\x1b')
        elif args.feature=='debug':
            send(':debug typo\r'); expect('Unknown debug profile')
            assert 'debug typo' not in history(), 'unknown debug profile entered history'
            send('B'); expect('Start debug container'); expect('Container: web')
            assert 'Confirm:' not in screen(raw,width,height,resize_events), 'pod name entry still required'
            send('c'); expect('<pod network>'); send('\x1b'); expect('Start debug container')
            send('d'); expect('runAsNonRoot'); send('d'); expect('Enter to start')
            send('\x1b'); expect('pods [12]')
            assert 'ephemeralContainers:' not in config.read_text(), 'cancel persisted debug state'
            send(':debug approved\r'); expect('Start debug container')
            expect('registry.kqt.invalid/debug:approved')
            send('\r'); expect('Demo debug terminal')
            send('\x1d'); expect('Disconnected; container may keep running')
            assert 'debug approved' in history(), 'successful debug command not saved'
            send(':debug attach\r'); expect('Demo debug terminal')
            send('exit\r'); expect('Debug session ended')
            send(':debug attach\r'); expect('Debug container failed'); expect('terminated')
            send('\x1b'); expect('pods [12]')
        elif args.feature=='command-history':
            assert history()==['layouts'], 'legacy config history was not migrated at startup'
            assert 'command_history' not in config.read_text(), 'migration retained legacy history in config'
            assert history_file.stat().st_mode & 0o777==0o600, 'cached history is not private'
            send(':theme nord\r'); send(':theme dracula\r')
            for value, error in [('themse','unknown resource'),('theme missing','Unknown theme'),('logs typo','Use :logs'),('pods typo','do not take arguments')]:
                send(':'+value+'\r'); expect(error)
            text=history()
            assert 'theme nord' in text and 'theme dracula' in text, 'successful commands were not saved'
            assert 'themse' not in text and 'theme missing' not in text and 'logs typo' not in text and 'pods typo' not in text, 'failed command entered history'
            assert 'command_history' not in config.read_text(), 'history was written back to config'
            before=config.read_bytes()
            send(':help\r'); expect('Help'); send('\x1b')
            assert config.read_bytes()==before and 'help' in history(), 'history-only command rewrote config'
            send(':draft\x12theme'); expect('(reverse-i-search)'); expect('Match: :theme dracula')
            send('\x12'); expect('Match: :theme nord')
            send('\x13'); expect('Match: :theme dracula')
            send('\t'); expect(':theme dracula'); send('\x1b')
            send(':draft\x12does-not-match\r'); expect('No matching command')
            send('\x1b'); expect(':draft'); send('\x1b')
            send(':\x12nord\r'); expect('· nord')
            send(':deploy\r'); expect('deployments.apps [1]')
            send(':scale 3\r'); expect('Confirm scale'); send('\x1b')
            assert 'scale 3' not in history(), 'cancelled mutation entered history'
            destination=root/'history-export.json'
            command=':export json '+json.dumps(str(destination))+'\r'
            send(command); expect('Manifest saved to'); before=history_file.read_bytes()
            send(command); expect('Unable to save manifest')
            assert history_file.read_bytes()==before, 'failed export changed saved command history'
            assert len(history())==5 and 'layouts' not in history(), 'configured history limit did not evict the oldest commands'
        elif args.feature=='contexts':
            send(':alias prod\r'); expect('Context: prod'); expect('Context alias saved:')
            assert 'prod: demo' in config.read_text(), 'alias was not saved'
            send(':ctx pr\t'); expect(':ctx prod'); send('\x1b')
            send('c'); expect('Contexts'); expect('prod'); expect('demo'); send('\r')
            expect('Context: prod'); expect('connected')
            send(':ctx prod\r'); expect('Context: prod'); expect('pods [12]')
            send(':alias\r'); expect('Context aliases'); expect('prod → demo'); send('\x1b')
            send(':unalias prod\r'); expect('Context: demo'); expect('Context alias removed:')
            assert 'prod: demo' not in config.read_text(), 'alias removal did not persist'
        elif args.feature=='rollouts':
            assert not args.kubeconfig, 'rollout smoke uses the isolated demo backend'
            send(':deploy\r'); expect('deployments.apps [1]')
            send('H'); expect('Rollouts'); expect('revision 2'); expect('current')
            send('\x12'); expect('Rollouts'); expect('revision 2')
            send('revision 1\r'); expect('Revision 1'); expect('nginx:1.27')
            send('d'); expect('/spec/containers/0/image'); expect('nginx:1.28')
            send('r'); expect('Confirm rollback')
            send('wrong\r'); expect('Type the exact resource name'); send('\x1b')
            send(':rollback 1\r'); expect('Confirm rollback')
            send('default/web\r'); expect('Rollback to revision 1 requested')
            destination=root/'rolled-back.json'
            send(':export json '+json.dumps(str(destination))+'\r'); expect('Manifest saved to')
            data=json.loads(destination.read_text())
            assert data['spec']['template']['spec']['containers'][0]['image']=='nginx:1.27', 'rollback did not restore the selected image'
            assert data['spec']['replicas']==12, 'rollback changed the replica count'
            send(':pods\r'); expect('pods [12]')
            send('H'); expect('Rollout history supports Deployments')
        elif args.feature=='log-queries':
            assert not args.kubeconfig, 'log query workflow uses isolated mixed-format demo logs'
            send('L'); expect('Containers'); send('\r'); expect('live heartbeat')
            send('F'); expect('snapshot')
            def filtered_export(query, name):
                send('/\x01\x0b'+query+'\r')
                target=root/name
                send('o'); expect('Save to:'); send('\x01\x0b'+str(target)+'\r')
                deadline=time.monotonic()+5
                while not target.exists() and time.monotonic()<deadline: pump()
                content=target.read_text()
                assert '\x1b' not in content, 'export retained styling'
                return content
            text=filtered_export('level>=warn', 'severity.log')
            assert 'WARN slow request' in text and 'INFO starting' not in text and 'request completed' not in text
            text=filtered_export('request_id=demo-123 http.status=200', 'json.log')
            assert 'request completed' in text and 'WARN' not in text
            text=filtered_export('re:"GET /" !health', 'regex.log')
            assert 'GET / 200' in text and '/health' not in text
            text=filtered_export('REQUEST COMPLETED', 'text.log')
            assert 'request completed' in text
            send('f'); send('/\x01\x0bre:[\r'); expect('Invalid filter'); send('\x1b')
            current=screen(raw,width,height,resize_events)
            assert 'request completed' in current and 'Context:' not in current
            send('f'); send('c'); expect('Containers'); send('\r'); expect('request completed')
            text=filtered_export('level=warn !health', 'preserved.log')
            send('\x12'); expect('WARN slow request')
            send('p'); expect('previous'); expect('WARN slow request')
            send('\x1b')
        elif args.feature=='log-options':
            assert not args.kubeconfig, 'log history workflow uses isolated demo logs'
            send('L'); expect('Containers'); send('\r'); expect('live heartbeat')
            send('O'); expect('Log options'); send('since 10m\r'); expect('last 10m')
            send('O'); send('tail 1000\r'); expect('1000 lines/source')
            send('T'); send('N')
            target=root/'displayed.log'
            send('o'); expect('Save to:'); send('\x01\x0b'+str(target)+'\r')
            deadline=time.monotonic()+5
            while not target.exists() and time.monotonic()<deadline: pump()
            content=target.read_text()
            assert content.startswith('[default/') and not re.search(r'\d{4}-\d\d-\d\dT',content), 'display/export lost timestamp or source preferences'
            text=config.read_text()
            assert 'since: 10m' in text and 'tail_lines: 1000' in text and 'timestamps: false' in text and re.search(r'prefixes: [\'"]?on[\'"]?',text), 'log defaults were not saved:\n'+text
            send('N'); send('f'); send('O'); expect('Log options'); send('tail custom\r'); expect('Lines per container')
            send('\x01\x0b-2\r'); expect('Invalid value'); send('\x1b')
            assert 'Context:' not in screen(raw,width,height,resize_events), 'custom option cancellation lost log focus'
            send('O'); send('since custom\r'); expect('Log window'); send('\x01\x0b1ns\r')
            expect('live heartbeat')
            assert 'configuration loaded' not in screen(raw,width,height,resize_events), 'custom time window returned old initial history'
            send('O'); send('since all\r'); expect('configuration loaded')
            send('O'); send('tail 100\r')
            assert 'tail_lines: 100\n' in config.read_text(), 'line preset was not applied in focus mode'
            send('O'); send('tail default\r')
            assert 'tail_lines:' not in config.read_text(), 'reset did not restore original per-mode defaults'
            send('w'); send('s'); send('f'); send('\x1b')
            send('L'); expect('Containers'); send('\r'); expect('follow off')
            assert '· wrap' not in screen(raw,width,height,resize_events).splitlines()[1], 'wrapping default was not restored on reopening'
            send('\x1b')
        elif args.feature=='logs-controls':
            send('L'); expect('Containers'); send('\r'); expect('live heartbeat')
            send('s'); expect('follow off')
            send('w')
            title=screen(raw,width,height,resize_events).splitlines()[1]
            assert '· wrap' not in title, 'w did not disable log wrapping'
            send('w')
            assert '· wrap' in screen(raw,width,height,resize_events).splitlines()[1], 'w did not enable wrapping'
            send('f'); pump(0.2)
            send('c'); expect('Containers'); send('\x1b'); pump(0.2)
            current=screen(raw,width,height,resize_events)
            assert 'Context:' not in current and '╭' not in current and 'Esc: close' not in current, 'focus retained application UI:\n'+current
            # Real SGR mouse press, drag, release. Arrival during a held drag
            # must not change the displayed text, even though collection runs.
            rows=current.splitlines()
            y=next(i for i,line in enumerate(rows) if 'listening' in line)
            x=rows[y].index('listening')
            def mouse(button,x,y,release=False):
                send(f'\x1b[<{button};{x+1};{y+1}'+('m' if release else 'M'))
            mouse(0,x,y); mouse(32,x+8,y)
            frozen=screen(raw,width,height,resize_events)
            pump(1.3)
            assert screen(raw,width,height,resize_events)==frozen, 'live arrival moved the held selection'
            mouse(0,x+8,y,True); expect('Copied')
            matches=re.findall(rb'\x1b]52;c;([A-Za-z0-9+/=]+)',raw)
            assert matches and base64.b64decode(matches[-1])==b'listening', 'drag copied UI, formatting or the wrong text'
            assert screen(raw,width,height,resize_events).splitlines()[-1].endswith(' Copied '), 'copy flash is not bottom-right'
            pump(1.5)
            assert 'Copied' not in screen(raw,width,height,resize_events), 'copy flash did not expire'
            send('/'); expect('/ '); send('listening\r'); expect('listening')
            send('/\x01\x0b\r'); expect('live heartbeat')
            for width in [80,120]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.2)
                current=expect('live heartbeat')
                assert 'Context:' not in current and '│' not in current, 'resize restored chrome in log focus'
            send('o'); expect('Save to:')
            target=root/'focused.log'
            send('\x01\x0b'+str(target)+'\r')
            deadline=time.monotonic()+5
            while not target.exists() and time.monotonic()<deadline: pump()
            assert 'live heartbeat' in target.read_text(), 'focus log export lost collected output'
            assert 'Save to:' not in screen(raw,width,height,resize_events), 'focus save prompt did not close'
            send('\x1b'); expect('Context:'); expect('follow off')
            send('s'); expect('following')
            send('F'); expect('snapshot'); send('F'); expect('live')
            send('\x1b')
        elif args.feature=='downloads':
            directory=root/'downloads with spaces'
            send(':download-dir '+json.dumps(str(directory))+'\r'); expect('Download directory:')
            assert str(directory) in config.read_text(), 'default download directory was not saved'
            assert not directory.exists(), 'setting the directory created it before an export'
            send(':export json clean nested/manifest.json\r'); expect('Manifest saved to')
            data=json.loads((directory/'nested/manifest.json').read_text())
            assert data['kind']=='Pod'
            name=data['metadata']['name']
            assert directory.stat().st_mode & 0o777==0o700, 'download directory is not private'
            manifest=name+'-pod.yaml'
            send('y'); expect('YAML'); send('o'); expect(manifest)
            for width in [80,120]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.2)
                expect(manifest)
            send('\r'); expect('Manifest saved to')
            assert 'kind: Pod' in (directory/manifest).read_text(), 'default manifest prompt saved to the wrong location'
            send('\x1b')
            send('M'); expect('Labels (')
            for tab, filename, text in [('2','kqt-labels.txt','Labels'),('3','kqt-annotations.txt','Annotations'),('4','kqt-finalizers.txt','Finalizers'),('1','kqt-events.log','Containers')]:
                send(tab); expect(text); send('o'); expect(filename); send('\r')
                expect('Events saved to' if tab=='1' else 'Metadata saved to')
                assert text in (directory/filename).read_text(), 'active resource tab used the wrong directory'
            send('\x1b')
            send('L'); expect('Containers'); send('\r'); expect('live heartbeat')
            filename=name+'-web.log'
            send(' '); send('o'); expect(filename); send('\r'); expect('Logs saved to')
            assert 'live heartbeat' in (directory/filename).read_text(), 'logs ignored the download directory'
            send('\x1b')
            status=(directory/'kqt-events.log').read_bytes()
            send(':events\r'); expect('events ['); send('o'); send('\r'); expect('Unable to save events')
            assert (directory/'kqt-events.log').read_bytes()==status, 'an event export overwrote a status export'
            send('o'); send('\x01\x0ball-events.log\r'); expect('Events saved to')
            assert 'BackOff' in (directory/'all-events.log').read_text(), 'global events ignored the default directory'
            send(':pods\r'); expect('pods [12]')
            override=root/'absolute.json'; send(':export json '+json.dumps(str(override))+'\r'); expect('Manifest saved to')
            assert json.loads(override.read_text())['kind']=='Pod', 'absolute export override failed'
            for path in directory.rglob('*'):
                if path.is_file(): assert path.stat().st_mode & 0o777==0o600, 'a downloaded file is not private'
            send(':download-dir clear\r'); expect('Download directory: current directory')
        elif args.feature=='relations':
            send(':deploy\r'); expect('deployments.apps [1]')
            send('b'); expect('Relations'); expect('owned ReplicaSet')
            send('owned ReplicaSet\r'); expect('replicasets.apps [1]')
            send('b'); expect('Relations'); send('Deployment default/web\r'); expect('deployments.apps [1]')
            send(':ing\r'); expect('ingresses.networking.k8s.io [1]')
            send(':related\r'); expect('ingress backend'); send('\r'); expect('services [1]')
            send('b'); expect('matching pod'); send('\x1b')
            send(':pvc\r'); expect('persistentvolumeclaims [1]')
            send('b'); expect('bound volume'); send('\r'); expect('persistentvolumes [1]')
            send('b'); expect('bound claim'); send('\x1b')
        elif args.feature=='overview':
            send('U'); expect('Cluster overview'); expect('Health')
            send('G'); expect('Recent warning events'); expect('BackOff')
            send('g'); expect('Node capacity')
            send('0'); expect('all namespaces'); expect('26 total')
            send('\r'); expect('Relations'); expect('CrashLoopBackOff'); send('\x1b')
            send(':overview\r'); expect('Cluster overview'); send('\x12'); expect('Health'); send('\x1b')
        elif args.feature=='access':
            expect('READ ONLY')
            send(':permissions\r'); expect('Permissions'); expect('demo-user'); expect('Allowed'); expect('disabled locally')
            send('\x1b')
            for action in ['edit','scale 2','restart','delete','shell']:
                send(':'+action+'\r'); expect('Read-only mode:')
            send(':can-i get\r'); expect('Allowed'); send('\x1b')
            target=root/'readonly.json'; send(':export json "'+str(target)+'"\r'); expect('Manifest saved to')
            assert json.loads(target.read_text())['kind']=='Pod'
        elif args.feature=='watch':
            expect('live')
            if args.kubeconfig:
                context=subprocess.check_output(['kubectl','--kubeconfig',str(args.kubeconfig.resolve()),'config','current-context'],text=True).strip()
                assert context.startswith('k3d-kqt-smoke-'), 'writes require disposable fixture'
                cli=['kubectl','--kubeconfig',str(args.kubeconfig.resolve()),'-n','kqt-test']
                send(':cm\r:labels app=kqt-feature-watch\r'); expect('configmaps [0]'); expect('live')
                obj={'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'kqt-feature-watch','namespace':'kqt-test','labels':{'app':'kqt-feature-watch'}},'data':{'value':'first'}}
                subprocess.run([*cli,'apply','-f','-'],input=json.dumps(obj),text=True,check=True,stdout=subprocess.DEVNULL)
                expect('configmaps [1]'); send('J'); expect('first')
                subprocess.run([*cli,'patch','configmap','kqt-feature-watch','--type=merge','-p','{"data":{"value":"second"}}'],check=True,stdout=subprocess.DEVNULL)
                expect('second'); send('\x1b')
                subprocess.run([*cli,'delete','configmap','kqt-feature-watch'],check=True,stdout=subprocess.DEVNULL)
                expect('configmaps [0]')
            else:
                send(':deploy\r'); expect('live')
                send(':scale 2\r'); expect('Confirm scale'); send('default/web\r'); expect('scale requested')
                send(':pods\r'); expect('live'); send(':nodes\r'); expect('nodes [3]'); expect('live')
        elif args.feature=='wide':
            assert not args.kubeconfig, 'wide PTY workflow uses the labelled demo nodes'
            send(':nodes\r'); expect('nodes [3]'); send('w'); expect('LABELS')
            for width in [80,240,320]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.3)
                current=screen(raw,width,height,resize_events)
                rows=current.splitlines()
                assert 'kubernetes.io/arch=arm64, kubernetes.io/hostname=' in current or width==80, 'wide labels lost comma spacing'
                if width==80:
                    send('l'); current=screen(raw,width,height,resize_events)
                    assert 'scroll' in current.splitlines()[1], 'narrow wide table lost horizontal scrolling'
                else:
                    assert 'standard-4' in rows[3], 'labels still truncate at the old 64-character cap'
                    assert 'scroll' not in rows[1], 'resize retained a horizontal offset despite spare space'
                    x=rows[2].index('LABELS')+1
                    send(f'\x1b[<0;{x};3M\x1b[<0;{x};3m')
                    assert 'LABELS ↑' in screen(raw,width,height,resize_events).splitlines()[2], 'expanded label header click sorted the wrong column'
                    send(':sort name asc\r')
                if width==320:
                    assert 'topology.kubernetes.io/zone=eu-west-2a' in rows[3], 'full labels are not shown when they fit'
            send(':theme no-color\r'); expect('LABELS')
            send(':theme accessible-light\r'); expect('LABELS')
            send('w')
            assert 'LABELS' not in screen(raw,width,height,resize_events), 'wide toggle left the extra columns behind'
        elif args.feature=='header':
            assert not args.kubeconfig, 'header PTY workflow uses the demo backend'
            for width,height,layout in [(80,24,'classic'),(180,48,'inspector'),(80,40,'bottom')]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.3)
                send(':layout '+layout+'\r'); expect('Context:')
                rows=screen(raw,width,height,resize_events).splitlines()
                assert rows[1].startswith('╭') and 'pods [' in rows[1], 'resource frame is not directly below the compact header'
                assert 'Context: demo' in rows[0] and 'connected' in rows[0], 'compact header lost context or connection state'
                assert 'Explorer' not in rows[0] and 'https://' not in rows[0], 'redundant header details remain'
                assert rows[-1].startswith(' Explorer · e: edit'), 'explorer help did not move to the footer'
                title='kqt · A Kubernetes Query Tool · DEMO ' if width>=120 else 'kqt · DEMO '
                assert rows[0].endswith(title), 'application title is not right-aligned'
                send('z'); expect('Header hidden')
                current=screen(raw,width,height,resize_events)
                assert current.splitlines()[0].startswith('╭') and 'Context:' not in current, 'hidden header left a gap'
                assert 'header_visible: false' in config.read_text(), 'hidden state was not saved'
                send(':sort name asc\r')
                header=screen(raw,width,height,resize_events).splitlines()[1]
                x=header.index('NAME')+1
                send(f'\x1b[<0;{x};2M\x1b[<0;{x};2m')
                assert 'NAME ↓' in screen(raw,width,height,resize_events).splitlines()[1], 'headerless column click failed'
                send('Rz'); expect('Context:')
                assert 'header_visible: true' in config.read_text(), 'shown state was not saved'
            send(':header off\r'); expect('Header hidden')
            send('L'); expect('Containers'); send('\r'); expect('live heartbeat')
            assert screen(raw,width,height,resize_events).splitlines()[0].startswith('╭'), 'logs retained the header gap'
            send('f')
            assert '╭' not in screen(raw,width,height,resize_events), 'logs-only focus retained a frame'
            send('f')
            assert screen(raw,width,height,resize_events).splitlines()[0].startswith('╭'), 'leaving logs-only focus lost hidden-header layout'
            send('z'); expect('Context:'); send('z'); expect('Header hidden'); send('\x1b')
            send('S'); expect('Status'); send('z'); expect('Context:'); send('z'); expect('Header hidden'); send('\x1b')
            send(':header typo\r'); expect('Use :header')
            assert 'header typo' not in history(), 'invalid command entered history'
            send('q')
            end=time.monotonic()+5
            while b'__kqt_exit__0' not in raw and time.monotonic()<end: pump()
            assert b'__kqt_exit__0' in raw and original==termios.tcgetattr(slave), 'first session did not restore terminal'
            send('\n'); process.wait(timeout=5)
            os.close(master); os.close(slave)
            master,slave=os.openpty()
            fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
            original=termios.tcgetattr(slave)
            raw.clear(); resize_events=[(0,width,height)]
            process=subprocess.Popen([sys.executable,'-c',wrapper,*launch],stdin=slave,stdout=slave,stderr=slave,preexec_fn=setup_child,env=env)
            expect('pods [12]')
            assert 'Context:' not in screen(raw,width,height,resize_events), 'restart lost hidden-header preference'
            send(':header on\r'); expect('Context:')
        elif args.feature=='paging':
            assert not args.kubeconfig, 'paging PTY workflow uses the demo backend'
            def expect_selected(index):
                current=screen(raw,width,height,resize_events)
                assert re.search(r'›\s+web-7c9f6d8b5-'+f'{index:04d}',current), 'wrong selected row:\n'+current
            for width,height,layout,page in [(80,12,'classic',6),(80,24,'bottom',11)]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.3)
                send(':layout '+layout+'\r'); send('g'); expect_selected(0)
                send('\x06'); expect_selected(page) # Ctrl+F, full page forward.
                send('\x02'); expect_selected(0) # Ctrl+B, full page backward.
                send('\x1b[6~'); expect_selected(page)
                send('\x1b[5~'); expect_selected(0)
                send('\x04'); expect_selected(page//2)
                send('\x15'); expect_selected(0)
            send('?'); expect('Navigation')
            send('\x06')
            assert 'Navigation' not in screen(raw,width,height,resize_events), 'Ctrl+F did not page help'
            send('\x02'); expect('Navigation'); send('\x1b')
            send(':themes\x02X'); expect(':themeXs'); send('\x1b')
        elif args.feature=='inspector':
            for width,height in [(180,48),(80,24)]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.3)
                send(':layout classic\r'); expect('classic')
                send('i'); expect('Inspector shown'); expect('Inspector · YAML')
                assert 'inspector_visible: true' in config.read_text()
                send('i'); expect('Inspector hidden')
                assert 'Inspector · YAML' not in screen(raw,width,height,resize_events)
                assert 'inspector_visible: false' in config.read_text()
                before_preview=config.read_text()
                send('I'); expect('Inspector position')
                if width>=120:
                    def inspector_title_row():
                        current=screen(raw,width,height,resize_events)
                        return next(i for i,line in enumerate(current.splitlines()) if 'Inspector · YAML' in line)
                    assert inspector_title_row()==1, 'right preview is not beside the list'
                    send('\x1b[B')
                    assert inspector_title_row()>1, 'arrow did not preview bottom placement'
                    send('\x1b[A')
                    assert inspector_title_row()==1, 'arrow did not preview right placement'
                    send(f'\x1b[<65;{width//2};{height//2}M')
                    assert inspector_title_row()>1, 'wheel did not preview bottom placement'
                assert config.read_text()==before_preview, 'preview changed the config'
                send('\x1b')
                assert 'Inspector · YAML' not in screen(raw,width,height,resize_events), 'cancel left a preview visible'
                assert 'inspector_visible: false' in config.read_text(), 'cancel changed visibility'
                send('Ibottom\r'); expect('Inspector shown at bottom'); expect('Inspector · YAML')
                assert 'inspector_position: bottom' in config.read_text()
                send('i'); expect('Inspector hidden'); send('i'); expect('Inspector shown at bottom')
                assert 'inspector_position: bottom' in config.read_text(), 'i cycled placement'
                send(':inspector\r'); expect('Inspector position'); send('right\r'); expect('Inspector shown')
                assert 'inspector_position: right' in config.read_text()
                send(':columns name,status,age\r:write inspector-test\r'); expect('Saved layout inspector-test')
                send('i'); expect('Inspector hidden'); send('i'); expect('Inspector shown')
                # Verify the saved view still owns its columns after visibility changes.
                assert 'columns:' in config.read_text() and 'layout: inspector-test' in config.read_text()
                send('b'); expect('Relations'); send('\x1b')
                send(':inspector off\r'); expect('Inspector hidden')
            send(':inspector typo\r'); expect('Use :inspector')
        elif args.feature=='help':
            for width,height in [(40,12),(80,24),(120,32),(180,48)]:
                resize_events.append((len(raw),width,height))
                fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',height,width,0,0))
                os.killpg(process.pid,signal.SIGWINCH); pump(0.4)
                send('?'); expect('Help'); expect('Navigation')
                if width>=80:
                    current=expect('Preferences')
                    header=next(line for line in current.splitlines() if 'Shortcuts' in line and 'Notes' in line)
                    assert header.index('Shortcuts')<header.index('Notes'), 'notes are not on the right'
                    send('\tG')
                    if width==80:
                        assert re.search(r'Notes.*100%',screen(raw,width,height,resize_events)), 'notes did not scroll independently'
                    send('\tg'); expect('Navigation')
                else:
                    send('\t'); expect('Preferences'); send('\t'); expect('Navigation')
                send('/rllts'); expect('/rllts'); expect('H / :rollouts')
                send('\r'); expect('fuzzy search')
                send('/\x15zzzzzzzzzzzz'); expect('No help matches')
                send('\x1b'); expect('/rllts'); expect('H / :rollouts')
                send('/\x15'); expect('Navigation')
                send('\x1b[200~osc52\x1b[201~'); expect('OSC52'); expect('/osc52')
                send('\x1b'); expect('/rllts')
                send('\x1b'); expect('pods')
                send(':help\r'); expect('Navigation')
                assert '/rllts' not in screen(raw,width,height,resize_events), 'help retained its previous search'
                send('\x1b')
            send(':themes\r'); expect('Themes'); send('\x1b')
        elif args.feature=='diagnostics':
            assert not args.kubeconfig, 'diagnostic PTY workflow uses the demo backend'
            send(':diagnose\r'); expect('Scheduling diagnostics'); expect('[5: Diagnostics]')
            send('G'); expect('Node observations do not prove a Pod')
            send('2'); expect('Labels ('); send('5'); expect('Scheduling diagnostics')
            send('\x1b'); send(':svc\r'); expect('services [1]')
            send(':diagnose\r'); expect('Service troubleshooting'); expect('Port mappings')
            target=root/'network.txt'
            send('o'); expect('Save to:'); send('\x15'+str(target)+'\r'); expect('saved to')
            report=target.read_text()
            assert 'ready published addresses' in report and 'web-7c9f6d8b5-0000' in report
            assert '\x1b' not in report
            send('\x1b'); send(':pods\r'); expect('pods [12]')
            downloads=root/'diagnostic exports'
            send(':download-dir "'+str(downloads)+'"\r'); expect('Download directory:')
            send(':bundle\r'); expect('Save bundle to:'); expect('diagnostics.tar.gz')
            send('\r'); expect('Diagnostic bundle saved to',timeout=20)
            import tarfile
            archives=list(downloads.glob('*.tar.gz')); assert len(archives)==1
            assert archives[0].stat().st_mode & 0o777==0o600
            with tarfile.open(archives[0]) as archive:
                names=archive.getnames()
                assert {'README.txt','resource.yaml','status.yaml','events.yaml','related.txt','diagnostics.txt'}<=set(names)
                assert 'logs.txt' not in names
            target=downloads/'with logs.tar.gz'
            send(':bundle --logs "'+str(target)+'"\r'); expect('Diagnostic bundle saved to',timeout=20)
            with tarfile.open(target) as archive: assert 'logs.txt' in archive.getnames()
            send(':bundle\r'); expect('Save bundle to:'); send('\x1b'); expect('Bundle export cancelled')
            send(':secrets\r'); expect('secrets [1]'); send(':bundle\r'); expect('Secret resources are excluded')
        elif args.feature=='actions':
            send(':deploy\r'); expect('deployments.apps [1]')
            send(':scale 3\r'); expect('Confirm scale')
            send('wrong\r'); expect('Type the exact resource name')
            send('\x1b'); expect('deployments.apps [1]')
            send(':scale 3\r'); expect('Confirm scale'); send('default/web\r'); expect('scale requested')
            target=root/'scaled.json'; send(':export json "'+str(target)+'"\r'); expect('Manifest saved to')
            assert json.loads(target.read_text())['spec']['replicas']==3
            send(':restart\r'); expect('Confirm restart'); send('default/web\r'); expect('restart requested')
            send(':cronjobs\r'); expect('cronjobs.batch [1]')
            for action in ['suspend','resume','run']:
                send(':'+action+'\r'); expect('Confirm '+action); send('default/nightly-report\r')
                expect('Created Job' if action=='run' else action+' requested')
            send(':jobs\r'); expect('jobs.batch [1]')
            send('D'); expect('Delete Job?'); expect('No'); expect('Yes')
            current=screen(raw,width,height,resize_events)
            top=next(i for i,line in enumerate(current.splitlines()) if 'Delete Job?' in line)
            assert top==(height-12)//2, 'delete modal is not compact and centred'
            send('\r'); expect('Deletion cancelled'); expect('jobs.batch [1]')
            send('D'); expect('Delete Job?'); send('\x1b[C\r'); expect('Deletion requested'); expect('jobs.batch [0]')
        elif args.feature=='manifest':
            destination=root/'clean manifest.json'
            send(':export json clean "'+str(destination)+'"\r'); expect('Manifest saved to')
            data=json.loads(destination.read_text())
            assert data['kind']=='Pod' and 'uid' not in data['metadata'] and 'status' not in data
            assert destination.stat().st_mode & 0o777==0o600
            send(':export json "'+str(destination)+'"\r'); expect('Unable to save manifest')
            assert json.loads(destination.read_text())==data
            send('C'); expect('sent to terminal clipboard')
            matches=re.findall(rb'\x1b]52;c;([A-Za-z0-9+/=]+)',raw)
            assert matches and b'kind: Pod' in base64.b64decode(matches[-1]), 'missing manifest clipboard request'
            send('J'); expect('JSON')
            send('o'); expect('Save manifest to:')
            target=root/'panel.json'
            send('\x01\x0b'+str(target)+'\r'); expect('Manifest saved to')
            assert json.loads(target.read_text())['kind']=='Pod'
            send('\x1b')
        else:
            raise AssertionError('unknown feature: '+args.feature)
        send('q')
        end=time.monotonic()+5
        while b'__kqt_exit__0' not in raw and time.monotonic()<end: pump()
        assert b'__kqt_exit__0' in raw,'unclean exit'
        assert original==termios.tcgetattr(slave),'terminal attributes not restored'
        send('\n'); process.wait(timeout=5)
        print('PASS feature PTY '+args.feature,flush=True)
    finally:
        if process.poll() is None:
            os.killpg(process.pid,signal.SIGTERM)
            try: process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid,signal.SIGKILL); process.wait()
        os.close(master); os.close(slave)
