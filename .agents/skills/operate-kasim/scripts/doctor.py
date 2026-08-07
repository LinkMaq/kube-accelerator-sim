#!/usr/bin/env python3
"""Read-only environment and Simulation Target inspection for operate-kasim."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from typing import Any


SUPPORTED_MINORS = range(30, 37)


def command_path(explicit: str | None, candidates: list[Path], name: str) -> str | None:
    if explicit:
        path = Path(explicit).expanduser().resolve()
        return str(path) if path.is_file() and os.access(path, os.X_OK) else None
    for candidate in candidates:
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return str(candidate.resolve())
    return shutil.which(name)


def run(
    command: list[str],
    timeout: int = 20,
    stdout_limit: int = 4000,
) -> dict[str, Any]:
    try:
        completed = subprocess.run(
            command,
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        return {"ok": False, "error": str(error)}
    result: dict[str, Any] = {
        "ok": completed.returncode == 0,
        "exitCode": completed.returncode,
    }
    if completed.stdout.strip():
        result["stdout"] = completed.stdout.strip()[:stdout_limit]
    if completed.stderr.strip():
        result["stderr"] = completed.stderr.strip()[:1000]
    return result


def parsed_json(result: dict[str, Any]) -> dict[str, Any] | None:
    if not result.get("ok") or "stdout" not in result:
        return None
    try:
        value = json.loads(result["stdout"])
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def load_json(path: Path) -> dict[str, Any] | None:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def kubernetes_minor(version: str) -> int | None:
    match = re.match(r"^v?1\.(\d+)", version)
    return int(match.group(1)) if match else None


def deployment_ready(item: dict[str, Any]) -> bool:
    desired = item.get("spec", {}).get("replicas", 1)
    available = item.get("status", {}).get("availableReplicas", 0)
    return desired == available


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Inspect Kasim tooling and an optional explicit Kubernetes target",
    )
    parser.add_argument("--repo-root", type=Path)
    parser.add_argument("--kasim-bin")
    parser.add_argument("--kubeconfig", type=Path)
    parser.add_argument("--context")
    args = parser.parse_args()

    if (args.kubeconfig is None) != (args.context is None):
        parser.error("--kubeconfig and --context must be supplied together")

    if args.repo_root is not None:
        repo_root = args.repo_root.expanduser().resolve()
    else:
        repo_root = Path(__file__).resolve().parents[4]
    kasim = command_path(
        args.kasim_bin,
        [repo_root / "dist" / "kasim", repo_root / "bin" / "kasim"],
        "kasim",
    )
    kubectl = shutil.which("kubectl")
    helm = shutil.which("helm")
    chart = repo_root / "charts" / "kasim-runtime"
    catalog_path = repo_root / "profiles" / "catalog.json"
    catalog_doc = load_json(catalog_path)
    catalog_revision = catalog_doc.get("revision") if catalog_doc else None
    vendor_examples = sorted((repo_root / "examples" / "vendors").glob("*.yaml"))

    report: dict[str, Any] = {
        "apiVersion": "operate-kasim/v1alpha1",
        "kind": "DoctorReport",
        "repoRoot": str(repo_root),
        "tools": {
            "kasim": {"path": kasim, "available": kasim is not None},
            "kubectl": {"path": kubectl, "available": kubectl is not None},
            "helm": {"path": helm, "available": helm is not None},
        },
        "repository": {
            "localChart": str(chart) if chart.is_dir() else None,
            "catalogRevision": catalog_revision,
            "vendorExampleCount": len(vendor_examples),
        },
        "target": None,
    }

    if kasim:
        version_result = run([kasim, "version", "-o", "json"])
        report["tools"]["kasim"]["versionCommand"] = version_result
        version_doc = parsed_json(version_result)
        report["tools"]["kasim"]["version"] = version_doc
        embedded_revision = None
        if version_doc:
            embedded_revision = version_doc.get("result", {}).get("catalogVersion")
        report["tools"]["kasim"]["embeddedCatalogRevision"] = embedded_revision
        report["tools"]["kasim"]["catalogMatchesRepository"] = (
            embedded_revision == catalog_revision
            if embedded_revision and catalog_revision
            else None
        )

    if args.kubeconfig is not None:
        kubeconfig = args.kubeconfig.expanduser().resolve()
        target: dict[str, Any] = {
            "kubeconfig": str(kubeconfig),
            "context": args.context,
            "kubeconfigExists": kubeconfig.is_file(),
            "reachable": False,
            "supported": False,
            "runtimeInstalled": False,
            "runtimeReady": False,
        }
        report["target"] = target
        if kubectl and kubeconfig.is_file():
            base = [kubectl, "--kubeconfig", str(kubeconfig), "--context", args.context]
            version_result = run([*base, "version", "-o", "json"])
            version_doc = parsed_json(version_result)
            target["versionCommand"] = version_result
            if version_doc:
                server_version = version_doc.get("serverVersion", {}).get("gitVersion", "")
                minor = kubernetes_minor(server_version)
                target["serverVersion"] = server_version
                target["reachable"] = bool(server_version)
                target["supported"] = minor in SUPPORTED_MINORS
                target["schedulingSupported"] = minor in SUPPORTED_MINORS
                target["stableDRASupported"] = minor is not None and 34 <= minor <= 36

            runtime_result = run(
                [
                    *base,
                    "--namespace",
                    "kasim-system",
                    "get",
                    "deployments",
                    "-l",
                    "app.kubernetes.io/instance=kasim-runtime",
                    "-o",
                    "json",
                ],
                stdout_limit=256_000,
            )
            runtime_doc = parsed_json(runtime_result)
            runtime_stdout = runtime_result.get("stdout", "")
            if len(runtime_stdout) > 4000:
                runtime_result["stdout"] = runtime_stdout[:4000]
                runtime_result["stdoutTruncated"] = True
            target["runtimeCommand"] = runtime_result
            if runtime_doc is not None:
                items = runtime_doc.get("items", [])
                target["runtimeInstalled"] = len(items) >= 2
                target["runtimeReady"] = len(items) >= 2 and all(
                    deployment_ready(item) for item in items
                )

    catalog_matches = report["tools"]["kasim"].get("catalogMatchesRepository")
    offline_ready = kasim is not None and catalog_matches is not False
    connected_ready = (
        report["target"] is not None
        and report["target"]["reachable"]
        and report["target"]["supported"]
    )
    report["ready"] = {
        "offlineCompile": offline_ready,
        "connectedOperations": connected_ready,
        "runtimeInstall": connected_ready and helm is not None and chart.is_dir(),
    }
    report["status"] = "ready" if offline_ready and (
        report["target"] is None or connected_ready
    ) else "attention-required"

    json.dump(report, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0 if report["status"] == "ready" else 1


if __name__ == "__main__":
    raise SystemExit(main())
