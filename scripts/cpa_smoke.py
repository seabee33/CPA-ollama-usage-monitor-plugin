#!/usr/bin/env python3
"""Integration check against a stock CPA binary; uses only synthetic keys.

Usage: python3 scripts/cpa_smoke.py /path/to/cli-proxy-api build/ollama-usage-monitor.so
No request to Ollama is made: the data test lists configured account names only.
"""
import json
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

binary = str(pathlib.Path(sys.argv[1]).resolve())
library = pathlib.Path(sys.argv[2]).resolve()
with tempfile.TemporaryDirectory(prefix="cpa-quota-test-") as directory:
    root = pathlib.Path(directory)
    plugins = root / "plugins" / "linux" / "amd64"
    plugins.mkdir(parents=True)
    shutil.copy2(library, plugins / "ollama-usage-monitor.so")
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    config = root / "config.yaml"
    config.write_text(f'''
host: "127.0.0.1"
port: {port}
auth-dir: "{root / 'auths'}"
remote-management:
  allow-remote: false
  secret-key: "synthetic-management-key"
  disable-control-panel: true
api-keys: ["synthetic-client-key"]
plugins:
  enabled: true
  dir: "{root / 'plugins'}"
  configs:
    ollama-usage-monitor:
      enabled: true
      dashboard_token: "synthetic-dashboard-token-32-characters"
      accounts:
        - name: "Personal"
          api_key: "synthetic-ollama-key"
openai-compatibility:
  - name: "Ollama"
    base-url: "https://ollama.com/v1"
    api-key-entries:
      - api-key: "synthetic-ollama-key"
    models:
      - name: "synthetic-model"
  - name: "Rejected"
    base-url: "https://not-ollama.invalid/v1"
    api-key-entries:
      - api-key: "synthetic-rejected-key"
    models:
      - name: "synthetic-model"
''')
    base = f"http://127.0.0.1:{port}"

    def request(path, payload=None, authenticated=True, dashboard_token=None):
        headers = {"Content-Type": "application/json"}
        if authenticated:
            headers["Authorization"] = "Bearer synthetic-management-key"
        if dashboard_token is not None:
            headers["X-Ollama-Monitor-Token"] = dashboard_token
        req = urllib.request.Request(base + path, headers=headers,
                                     data=None if payload is None else json.dumps(payload).encode())
        try:
            with urllib.request.urlopen(req, timeout=5) as response:
                return response.status, response.read().decode()
        except urllib.error.HTTPError as error:
            return error.code, error.read().decode()

    with (root / "server.log").open("w+") as log:
        process = subprocess.Popen([binary, "--config", str(config), "--local-model"],
                                   cwd=root, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 30
            while True:
                try:
                    status, body = request("/v0/resource/plugins/ollama-usage-monitor/dashboard", authenticated=False)
                    if status == 200:
                        break
                except (OSError, urllib.error.URLError):
                    pass
                if process.poll() is not None or time.monotonic() >= deadline:
                    log.seek(0)
                    raise RuntimeError("CPA failed to start: " + log.read())
                time.sleep(0.1)
            assert 'Unlock your dashboard' in body and 'synthetic-ollama-key' not in body
            resource = '/v0/resource/plugins/ollama-usage-monitor'
            for asset in ('/app.js', '/style.css'):
                status, body = request(resource+asset, authenticated=False)
                assert status == 200 and len(body) > 100
            for key in (None, 'incorrect'):
                status, body = request(resource+'/data', authenticated=False, dashboard_token=key)
                assert status == 401 and 'Personal' not in body, (status, body)
            # CPA management authentication is deliberately not sufficient for this route.
            status, _ = request(resource+'/data')
            assert status == 401
            status, body = request(resource+'/data', authenticated=False, dashboard_token='synthetic-dashboard-token-32-characters')
            assert status == 200 and json.loads(body)['accounts'] == ['Personal'], (status, body)
            assert 'synthetic-ollama-key' not in body
            status, _ = request(resource+'/data?token=synthetic-dashboard-token-32-characters', authenticated=False)
            assert status == 401
            print('Stock CPA integration passed: sidebar assets, public-shell redaction, token-protected account listing, query-token rejection')
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
