#!/usr/bin/env python3
"""Prove the demo external-auth and volume backup/restore paths end to end."""

from __future__ import annotations

import os
import re
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import urljoin

import requests
from bs4 import BeautifulSoup

STATE_FILE = Path(os.environ.get("DEMO_ENV", "/tmp/opencode/poddy-demo.env"))
KUBECONFIG = os.environ.get("PODDY_KUBECONFIG", "/tmp/opencode/k3s.yaml")
WORKSPACE = os.environ.get("DEMO_WORKSPACE", "demo-ext")
KEEP_WORKSPACE = os.environ.get("KEEP_DEMO_WORKSPACE", "0") == "1"


def load_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value.strip().strip("'\"")
    return values


def kubectl(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["kubectl", "--kubeconfig", KUBECONFIG, *args],
        check=check,
        text=True,
        capture_output=True,
    )


def csrf_from(html: str) -> str:
    soup = BeautifulSoup(html, "html.parser")
    field = soup.find("input", {"name": "_csrf"})
    if field is None or not field.get("value"):
        raise RuntimeError("Gitea login page has no _csrf field")
    return str(field["value"])


def form_data(html: str, action_contains: str) -> tuple[str, dict[str, str]]:
    soup = BeautifulSoup(html, "html.parser")
    for form in soup.find_all("form"):
        action = str(form.get("action") or "")
        if action_contains not in action:
            continue
        data = {}
        for field in form.find_all("input"):
            name = field.get("name")
            if name:
                data[str(name)] = str(field.get("value") or "")
        return action, data
    raise RuntimeError(f"form containing {action_contains!r} not found")


def gitea_login(session: requests.Session, gitea_url: str, user: str, password: str) -> None:
    page = session.get(f"{gitea_url}/user/login", timeout=20)
    page.raise_for_status()
    csrf = csrf_from(page.text)
    response = session.post(
        f"{gitea_url}/user/login",
        data={"_csrf": csrf, "user_name": user, "password": password, "remember": "on"},
        allow_redirects=False,
        timeout=20,
    )
    if response.status_code not in (301, 302, 303):
        raise RuntimeError(f"Gitea login failed: HTTP {response.status_code}")
    check = session.get(f"{gitea_url}/", timeout=20)
    if check.status_code != 200 or "/user/logout" not in check.text:
        raise RuntimeError(f"Gitea session is not authenticated: HTTP {check.status_code}")
    print(f"[ok] signed in to Gitea as {user}")


def gitea_authorize(session: requests.Session, gitea_url: str, auth_url: str) -> str:
    response = session.get(auth_url, allow_redirects=False, timeout=20)
    if response.status_code in (301, 302, 303):
        return response.headers["Location"]

    if response.status_code != 200:
        raise RuntimeError(f"Gitea authorize returned HTTP {response.status_code}")

    action, data = form_data(response.text, "/login/oauth/grant")
    data["granted"] = "true"
    response = session.post(
        urljoin(gitea_url, action),
        data=data,
        allow_redirects=False,
        timeout=20,
    )
    if response.status_code not in (301, 302, 303):
        raise RuntimeError(f"Gitea consent failed: HTTP {response.status_code}")
    return response.headers["Location"]


def wait_workspace(portal: requests.Session, portal_url: str, name: str, phase: str, timeout: float = 240) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        response = portal.get(f"{portal_url}/api/workspaces/{name}", timeout=20)
        if response.status_code == 200:
            current = response.json().get("phase", "")
            if current == phase:
                print(f"[ok] workspace {name} is {phase}")
                return
        time.sleep(2)
    raise RuntimeError(f"workspace {name} did not reach {phase}")


def workspace_pod(name: str) -> str:
    return f"poddy-{name}-0"


def exec_workspace(name: str, command: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return kubectl(
        "-n",
        "poddy-workspaces",
        "exec",
        workspace_pod(name),
        "-c",
        "workspace",
        "--",
        "sh",
        "-c",
        command,
        check=check,
    )


def main() -> int:
    env = load_env(STATE_FILE)
    host = env["DEMO_HOST"]
    portal_url = env["PORTAL_URL"].rstrip("/")
    gitea_url = env["GITEA_URL"].rstrip("/")
    token = env["PORTAL_TOKEN"]

    portal = requests.Session()
    portal.headers["Authorization"] = f"Bearer {token}"

    print(f"==> Portal {portal_url}")
    print(f"==> Gitea  {gitea_url}")

    status = portal.get(f"{portal_url}/api/external-auth", params={"ids": "gitea"}, timeout=20)
    status.raise_for_status()
    providers = status.json().get("providers") or []
    if not providers or providers[0].get("id") != "gitea":
        raise RuntimeError(f"Gitea provider is not configured: {status.text}")
    print(f"[ok] provider configured: {providers[0].get('displayName')}")

    login = portal.get(
        f"{portal_url}/external-auth/gitea/login",
        params={"next": "/"},
        allow_redirects=False,
        timeout=20,
    )
    if login.status_code != 303:
        raise RuntimeError(f"external auth login returned HTTP {login.status_code}: {login.text}")
    auth_url = login.headers["Location"]
    print("[ok] portal redirected to Gitea authorization")

    gitea = requests.Session()
    gitea_login(gitea, gitea_url, env["GITEA_ADMIN_USER"], env["GITEA_ADMIN_PASSWORD"])
    callback = gitea_authorize(gitea, gitea_url, auth_url)
    print("[ok] Gitea authorized the poddy OAuth2 application")

    callback_response = portal.get(callback, allow_redirects=False, timeout=30)
    if callback_response.status_code != 303:
        raise RuntimeError(
            f"portal callback returned HTTP {callback_response.status_code}: {callback_response.text}"
        )

    status = portal.get(f"{portal_url}/api/external-auth", params={"ids": "gitea"}, timeout=20)
    status.raise_for_status()
    if not status.json()["providers"][0].get("authenticated"):
        raise RuntimeError(f"Gitea is still not connected: {status.text}")
    print("[ok] portal stored a Gitea token and reports authenticated=true")

    existing = portal.get(f"{portal_url}/api/workspaces", timeout=20)
    stale = []
    if existing.status_code == 200:
        for item in existing.json():
            name = str(item.get("name") or "")
            if name == WORKSPACE or name.endswith(f"-{WORKSPACE}"):
                portal.delete(f"{portal_url}/api/workspaces/{name}", timeout=30)
                stale.append(name)
    for name in stale:
        deadline = time.time() + 120
        while time.time() < deadline:
            workspace_gone = portal.get(f"{portal_url}/api/workspaces/{name}", timeout=20).status_code == 404
            pvc_gone = kubectl(
                "-n", "poddy-workspaces", "get", "pvc", f"poddy-{name}", check=False
            ).returncode != 0
            if workspace_gone and pvc_gone:
                break
            time.sleep(2)
        else:
            raise RuntimeError(f"stale workspace {name} did not finish deleting")

    created = portal.post(
        f"{portal_url}/api/workspaces",
        json={
            "name": WORKSPACE,
            "template": "gitea",
            "parameters": {
                "cpu": "250m",
                "memory": "512Mi",
                "storage": "1Gi",
                "browserVSCode": "false",
            },
        },
        timeout=60,
    )
    if created.status_code != 201:
        raise RuntimeError(f"workspace create failed: HTTP {created.status_code}: {created.text}")
    workspace = created.json()["name"]
    print(f"[ok] created workspace {workspace} with the gitea template")

    wait_workspace(portal, portal_url, workspace, "Running")

    env_names = kubectl(
        "-n",
        "poddy-workspaces",
        "get",
        "statefulset",
        f"poddy-{workspace}",
        "-o",
        'jsonpath={.spec.template.spec.containers[?(@.name=="workspace")].env[*].name}',
    ).stdout
    for required in ("GITEA_TOKEN", "PODDY_EXTERNAL_AUTH_GITEA_TOKEN", "PODDY_PORTAL_URL"):
        if required not in env_names.split():
            raise RuntimeError(f"{required} missing from workspace environment: {env_names}")
    print("[ok] workspace env contains GITEA_TOKEN and PODDY_EXTERNAL_AUTH_GITEA_TOKEN")

    workspace_token = kubectl(
        "-n",
        "poddy-workspaces",
        "get",
        "statefulset",
        f"poddy-{workspace}",
        "-o",
        'jsonpath={.spec.template.spec.containers[?(@.name=="workspace")].env[?(@.name=="PODDY_WORKSPACE_TOKEN")].value}',
    ).stdout
    if not workspace_token:
        raise RuntimeError("PODDY_WORKSPACE_TOKEN is empty")
    refreshed = requests.get(
        f"{portal_url}/api/workspaces/{workspace}/external-auth/gitea/token",
        headers={"X-Poddy-Workspace-Token": workspace_token},
        timeout=20,
    )
    refreshed.raise_for_status()
    if not refreshed.json().get("accessToken"):
        raise RuntimeError(f"workspace token endpoint returned no token: {refreshed.text}")
    print("[ok] workspace can fetch a refreshed Gitea token from the portal")

    cli_status = exec_workspace(workspace, "poddy external-auth status gitea").stdout.strip()
    if cli_status != "gitea: connected" and not cli_status.startswith("gitea: connected ("):
        raise RuntimeError(f"poddy external-auth status failed: {cli_status!r}")
    cli_token = exec_workspace(workspace, "poddy external-auth token gitea").stdout.strip()
    if not cli_token:
        raise RuntimeError("poddy external-auth token returned no token")
    askpass = exec_workspace(
        workspace,
        f"poddy git-askpass \"Password for '{gitea_url}':\"",
    ).stdout.strip()
    if not askpass:
        raise RuntimeError("poddy git-askpass returned no token")
    print("[ok] workspace CLI reports status, refreshes, and passes the token to Git")

    disconnect = portal.post(f"{portal_url}/api/external-auth/gitea/disconnect", timeout=20)
    if disconnect.status_code != 204:
        raise RuntimeError(f"disconnect failed: HTTP {disconnect.status_code}: {disconnect.text}")
    exec_workspace(
        workspace,
        "rm -f /tmp/poddy-login.out /tmp/poddy-login.code; "
        "nohup sh -c 'poddy external-auth login --no-open --timeout 2m gitea "
        "> /tmp/poddy-login.out 2>&1; echo $? > /tmp/poddy-login.code' "
        "</dev/null >/dev/null 2>&1 &",
    )
    login_url = None
    deadline = time.time() + 30
    while time.time() < deadline and login_url is None:
        output = exec_workspace(
            workspace, "cat /tmp/poddy-login.out 2>/dev/null || true", check=False
        ).stdout
        match = re.search(r"https?://\S+/external-auth/gitea/cli\?\S+", output)
        if match:
            login_url = match.group(0)
            break
        time.sleep(1)
    if not login_url:
        raise RuntimeError("workspace CLI did not print a login URL")

    browser = requests.Session()
    browser.headers["Authorization"] = f"Bearer {token}"
    start = browser.get(login_url, allow_redirects=False, timeout=30)
    if start.status_code != 303:
        raise RuntimeError(f"CLI login URL returned HTTP {start.status_code}: {start.text}")
    callback = gitea_authorize(gitea, gitea_url, start.headers["Location"])
    finished = browser.get(callback, allow_redirects=False, timeout=30)
    if finished.status_code != 303:
        raise RuntimeError(f"CLI callback returned HTTP {finished.status_code}: {finished.text}")

    code = ""
    deadline = time.time() + 30
    while time.time() < deadline and not code:
        code = exec_workspace(
            workspace, "cat /tmp/poddy-login.code 2>/dev/null || true", check=False
        ).stdout.strip()
        if not code:
            time.sleep(1)
    if code != "0":
        output = exec_workspace(
            workspace, "cat /tmp/poddy-login.out 2>/dev/null || true", check=False
        ).stdout
        raise RuntimeError(f"workspace CLI login exited {code}: {output}")
    print("[ok] workspace CLI login completed through the browser flow")

    proof = f"poddy-demo-{int(time.time())}"
    exec_workspace(workspace, f"printf '%s\\n' '{proof}' > /home/dev/backup-proof.txt")
    print("[ok] wrote /home/dev/backup-proof.txt")

    stop = portal.post(f"{portal_url}/api/workspaces/{workspace}/stop", timeout=60)
    if stop.status_code != 200:
        raise RuntimeError(f"workspace stop failed: HTTP {stop.status_code}: {stop.text}")
    wait_workspace(portal, portal_url, workspace, "Stopped")

    volume = f"poddy-{workspace}"
    backup = portal.post(f"{portal_url}/api/volumes/{volume}/backups", timeout=300)
    if backup.status_code != 201:
        raise RuntimeError(f"backup failed: HTTP {backup.status_code}: {backup.text}")
    key = backup.json()["key"]
    print(f"[ok] backup created: {key}")

    backups = portal.get(f"{portal_url}/api/volumes/{volume}/backups", timeout=30)
    backups.raise_for_status()
    if not any(item.get("key") == key for item in backups.json()):
        raise RuntimeError(f"backup is not listed: {backups.text}")
    print("[ok] backup is listed in MinIO")

    start = portal.post(f"{portal_url}/api/workspaces/{workspace}/start", timeout=60)
    if start.status_code != 200:
        raise RuntimeError(f"workspace start failed: HTTP {start.status_code}: {start.text}")
    wait_workspace(portal, portal_url, workspace, "Running")

    exec_workspace(workspace, "rm -f /home/dev/backup-proof.txt")
    missing = exec_workspace(workspace, "test ! -e /home/dev/backup-proof.txt", check=False)
    if missing.returncode != 0:
        raise RuntimeError("could not remove backup-proof.txt before restore")
    print("[ok] removed the proof file before restore")

    stop = portal.post(f"{portal_url}/api/workspaces/{workspace}/stop", timeout=60)
    if stop.status_code != 200:
        raise RuntimeError(f"workspace stop failed: HTTP {stop.status_code}: {stop.text}")
    wait_workspace(portal, portal_url, workspace, "Stopped")

    restore = portal.post(
        f"{portal_url}/api/volumes/{volume}/restore",
        json={"key": key, "clean": True},
        timeout=300,
    )
    if restore.status_code != 204:
        raise RuntimeError(f"restore failed: HTTP {restore.status_code}: {restore.text}")
    print("[ok] restore completed")

    start = portal.post(f"{portal_url}/api/workspaces/{workspace}/start", timeout=60)
    if start.status_code != 200:
        raise RuntimeError(f"workspace start failed: HTTP {start.status_code}: {start.text}")
    wait_workspace(portal, portal_url, workspace, "Running")

    restored = exec_workspace(workspace, "cat /home/dev/backup-proof.txt").stdout.strip()
    if restored != proof:
        raise RuntimeError(f"restored content mismatch: {restored!r} != {proof!r}")
    print(f"[ok] restored /home/dev/backup-proof.txt: {restored}")

    if not KEEP_WORKSPACE:
        portal.delete(f"{portal_url}/api/workspaces/{workspace}", timeout=60)
        print("[ok] removed the verification workspace")

    print("\nPROOF OK: Gitea external auth, token injection, refresh, backup and restore all work.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        print(f"PROOF FAILED: {exc}", file=sys.stderr)
        sys.exit(1)
