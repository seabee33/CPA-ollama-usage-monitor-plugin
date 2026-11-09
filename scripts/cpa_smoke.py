#!/usr/bin/env python3
"""Integration check against a patched CPA binary; uses only synthetic keys.

Usage: python3 scripts/cpa_smoke.py /path/to/cli-proxy-api build/ollama-usage-monitor.so
No request to Ollama is made: the quota-fetch test uses a rejected base URL.
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
      provider_names: ["Ollama", "Rejected"]
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
    base = f"http://127.0.0.1:{port}/v8/management"

    def request(path, payload=None, authenticated=True):
        headers = {"Content-Type": "application/json"}
        if authenticated:
            headers["Authorization"] = "Bearer synthetic-management-key"
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
                    status, body = request("/credentials")
                    if status == 200:
                        break
                except (OSError, urllib.error.URLError):
                    pass
                if process.poll() is not None or time.monotonic() >= deadline:
                    log.seek(0)
                    raise RuntimeError("CPA failed to start: " + log.read())
                time.sleep(0.1)
            files = json.loads(body)["files"]
            assert len(files) == 2, body
            for item in files:
                assert item["supports_quota"] is True, item
                assert item["quota_plugin_id"] == "ollama-usage-monitor", item
            assert "synthetic-ollama-key" not in body and "synthetic-rejected-key" not in body
            status, _ = request("/credentials", authenticated=False)
            assert status in (401, 403), status
            rejected = next(f for f in files if f["provider"] == "openai-compatible-rejected")
            path = "/plugins/ollama-usage-monitor/quota"
            status, body = request(path, {"auth_index": rejected["auth_index"]})
            assert status == 502 and "select an Ollama Cloud credential" in body, (status, body)
            status, _ = request(path, {"auth_index": rejected["auth_index"]}, authenticated=False)
            assert status in (401, 403), status
            print("CPA integration passed: plugin discovery, configured credential visibility, secret redaction, authenticated quota dispatch")
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
